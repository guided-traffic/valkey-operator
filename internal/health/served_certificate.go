package health

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"

	"github.com/guided-traffic/valkey-operator/internal/builder"
)

// observeServedCertificate arms cfg with a report-only VerifyConnection hook
// that compares the leaf certificate a pod actually serves against the tls.crt
// of the Secret it mounts (T28). The health pass dials every pod anyway, so the
// observation costs no extra connection.
//
// The ADR 0030 fingerprint can only ever answer "was the pod replaced"; this
// hook is the one observation of the running process itself, which is the
// failure that ADR exists for. What the two verdicts mean:
//
//   - A mismatch after a rotation, before the roll reaches the pod, is the
//     expected shape: the process pins the material it parsed at startup (D6).
//     Logged at Info -- it appears for the minutes a rotation roll is in
//     flight and is otherwise the signal that a roll is not happening.
//   - A pod serving the NEW leaf although it has not been replaced since the
//     rotation would prove the process reloads on its own -- the measurement
//     that could one day relax D6 and spare every metrics-free TLS cluster a
//     roll per renewal. The match is logged at V(1), so that experiment is run
//     by raising the log level instead of by spamming a healthy fleet.
//
// Strictly report-only, twice over: the hook never fails a handshake -- ADR
// 0030 D4 allows no new replacement mechanism, and an observation must not be
// able to turn into an outage -- and it does not authenticate the mount: after
// a hostile Secret swap has propagated, served leaf and Secret agree again
// (ADR 0030 D11).
func observeServedCertificate(logger logr.Logger, cfg *tls.Config, secret *corev1.Secret) {
	leafPEM, ok := secret.Data[builder.TLSCertKey]
	if !ok {
		return
	}
	block, _ := pem.Decode(leafPEM)
	if block == nil {
		return
	}
	expected, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return
	}

	cfg.VerifyConnection = func(cs tls.ConnectionState) error {
		if len(cs.PeerCertificates) == 0 {
			return nil
		}
		served := cs.PeerCertificates[0]
		if bytes.Equal(served.Raw, expected.Raw) {
			logger.V(1).Info("pod serves the certificate its Secret currently holds",
				"peer", cs.ServerName, "serial", served.SerialNumber.String())
			return nil
		}
		logger.Info("pod serves a TLS certificate that differs from the one in its Secret",
			"peer", cs.ServerName,
			"servedSerial", served.SerialNumber.String(),
			"servedNotAfter", served.NotAfter.UTC().Format(time.RFC3339),
			"secretSerial", expected.SerialNumber.String())
		return nil
	}
}
