package observer

import (
	"crypto/tls"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildTLSConfig_CACertOnly(t *testing.T) {
	caPath, _, _ := writeTestCerts(t)

	cfg, err := buildTLSConfig(Config{TLSCACert: caPath}, false)

	require.NoError(t, err)
	assert.Equal(t, uint16(tls.VersionTLS12), cfg.MinVersion)
	assert.NotNil(t, cfg.RootCAs, "the CA bundle must be loaded for server verification")
	assert.Empty(t, cfg.Certificates, "without mTLS no client certificate is presented")
}

func TestBuildTLSConfig_ClientCertificateIsLoadedForMTLS(t *testing.T) {
	caPath, certPath, keyPath := writeTestCerts(t)

	cfg, err := buildTLSConfig(Config{TLSCACert: caPath, TLSCert: certPath, TLSKey: keyPath}, true)

	require.NoError(t, err)
	assert.NotNil(t, cfg.RootCAs)
	require.Len(t, cfg.Certificates, 1, "mTLS must present exactly one client certificate")
	assert.NotNil(t, cfg.Certificates[0].PrivateKey)
}

// The client certificate is only loaded when both paths are configured; a
// half-configured pair must not fail the observer at startup.
func TestBuildTLSConfig_MTLSWithoutCertPathsIsNotAnError(t *testing.T) {
	caPath, _, keyPath := writeTestCerts(t)

	cfg, err := buildTLSConfig(Config{TLSCACert: caPath, TLSKey: keyPath}, true)

	require.NoError(t, err)
	assert.Empty(t, cfg.Certificates)
}

func TestBuildTLSConfig_NoCACertUsesSystemRoots(t *testing.T) {
	cfg, err := buildTLSConfig(Config{}, false)

	require.NoError(t, err)
	assert.Nil(t, cfg.RootCAs, "an empty CA path leaves verification to the system roots")
	assert.Equal(t, uint16(tls.VersionTLS12), cfg.MinVersion)
}

func TestBuildTLSConfig_Errors(t *testing.T) {
	caPath, certPath, keyPath := writeTestCerts(t)

	t.Run("missing CA file", func(t *testing.T) {
		_, err := buildTLSConfig(Config{TLSCACert: caPath + ".absent"}, false)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "reading CA cert")
	})

	t.Run("CA file that is not a certificate", func(t *testing.T) {
		broken := caPath + ".broken"
		require.NoError(t, os.WriteFile(broken, []byte("not a PEM block"), 0o600))

		_, err := buildTLSConfig(Config{TLSCACert: broken}, false)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to parse CA certificate")
	})

	t.Run("client key that does not parse", func(t *testing.T) {
		brokenKey := keyPath + ".broken"
		require.NoError(t, os.WriteFile(brokenKey, []byte("not a PEM block"), 0o600))

		_, err := buildTLSConfig(Config{TLSCACert: caPath, TLSCert: certPath, TLSKey: brokenKey}, true)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "loading client certificate")
	})
}

// Valkey and Sentinel get separate TLS configs on purpose: the operator sends a
// client certificate to Valkey but, by default, only verifies the Sentinel
// server. A shared config would leak the client certificate to Sentinel.
func TestNew_SentinelTLSConfigOmitsTheClientCertificate(t *testing.T) {
	caPath, certPath, keyPath := writeTestCerts(t)

	obs, err := New(Config{
		ClusterName:     "test",
		TLSEnabled:      true,
		TLSCACert:       caPath,
		TLSCert:         certPath,
		TLSKey:          keyPath,
		ValkeyMTLS:      true,
		SentinelMTLS:    false,
		SentinelEnabled: true,
	})

	require.NoError(t, err)
	require.NotNil(t, obs.tlsConfig)
	require.NotNil(t, obs.sentinelTLSConfig)
	assert.Len(t, obs.tlsConfig.Certificates, 1, "Valkey connections use mTLS")
	assert.Empty(t, obs.sentinelTLSConfig.Certificates, "Sentinel connections must not present a client certificate")
	assert.NotNil(t, obs.sentinelTLSConfig.RootCAs, "the Sentinel server is still verified")
}

func TestNew_SentinelMTLSLoadsItsOwnClientCertificate(t *testing.T) {
	caPath, certPath, keyPath := writeTestCerts(t)

	obs, err := New(Config{
		ClusterName:     "test",
		TLSEnabled:      true,
		TLSCACert:       caPath,
		TLSCert:         certPath,
		TLSKey:          keyPath,
		ValkeyMTLS:      true,
		SentinelMTLS:    true,
		SentinelEnabled: true,
	})

	require.NoError(t, err)
	assert.Len(t, obs.sentinelTLSConfig.Certificates, 1)
}

func TestNew_WithoutSentinelNoSentinelTLSConfigIsBuilt(t *testing.T) {
	caPath, certPath, keyPath := writeTestCerts(t)

	obs, err := New(Config{
		ClusterName:     "test",
		TLSEnabled:      true,
		TLSCACert:       caPath,
		TLSCert:         certPath,
		TLSKey:          keyPath,
		ValkeyMTLS:      true,
		SentinelEnabled: false,
	})

	require.NoError(t, err)
	assert.NotNil(t, obs.tlsConfig)
	assert.Nil(t, obs.sentinelTLSConfig)
}

// A broken Sentinel client certificate must be reported as such rather than as a
// Valkey TLS problem.
func TestNew_SentinelTLSFailureIsReportedSeparately(t *testing.T) {
	caPath, certPath, keyPath := writeTestCerts(t)
	brokenKey := keyPath + ".broken"
	require.NoError(t, os.WriteFile(brokenKey, []byte("not a PEM block"), 0o600))

	obs, err := New(Config{
		ClusterName:     "test",
		TLSEnabled:      true,
		TLSCACert:       caPath,
		TLSCert:         certPath,
		TLSKey:          brokenKey,
		ValkeyMTLS:      false,
		SentinelMTLS:    true,
		SentinelEnabled: true,
	})

	require.Error(t, err)
	assert.Nil(t, obs)
	assert.Contains(t, err.Error(), "building sentinel TLS config")
}
