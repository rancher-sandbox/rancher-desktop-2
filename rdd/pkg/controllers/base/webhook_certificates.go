// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package base

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"

	"k8s.io/klog/v2"
)

const (
	// DefaultWebhookCACertFileName is the default filename for webhook CA certificates.
	DefaultWebhookCACertFileName = "webhook-ca.crt"
	defaultWebhookCAKeyFileName  = "webhook-ca.key"
)

// SharedWebhookCertificateManager handles webhook certificate generation and management
// for multiple controllers sharing the same webhook server infrastructure.
type SharedWebhookCertificateManager struct {
	certDir      string
	certName     string
	keyName      string
	serverIP     string
	serviceNames []string // DNS names for all webhook services
}

// NewSharedWebhookCertificateManager creates a new shared certificate manager.
func NewSharedWebhookCertificateManager(certDir, certName, keyName, serverIP string, serviceNames []string) *SharedWebhookCertificateManager {
	return &SharedWebhookCertificateManager{
		certDir:      certDir,
		certName:     certName,
		keyName:      keyName,
		serverIP:     serverIP,
		serviceNames: serviceNames,
	}
}

// GenerateWebhookCertificates generates self-signed webhook server certificates
// with SANs for all registered webhook services.
func (cm *SharedWebhookCertificateManager) GenerateWebhookCertificates() error {
	klog.V(2).Info("Generating shared webhook certificates")

	var caKey *rsa.PrivateKey
	var caCert *x509.Certificate
	var err error

	// If possible, load the CA private key and certificate from existing files.
	if caKeyBytes, err := os.ReadFile(filepath.Join(cm.certDir, defaultWebhookCAKeyFileName)); err == nil {
		block, _ := pem.Decode(caKeyBytes)
		if block != nil && block.Type == "RSA PRIVATE KEY" {
			caKey, err = x509.ParsePKCS1PrivateKey(block.Bytes)
			if err != nil {
				klog.V(2).Info("Failed to parse existing CA private key, will regenerate", "error", err)
				caKey = nil
			}
		}
	}
	if caCertBytes, err := os.ReadFile(filepath.Join(cm.certDir, DefaultWebhookCACertFileName)); err == nil {
		block, _ := pem.Decode(caCertBytes)
		if block != nil && block.Type == "CERTIFICATE" {
			caCert, err = x509.ParseCertificate(block.Bytes)
			if err != nil {
				klog.V(2).Info("Failed to parse existing CA certificate, will regenerate", "error", err)
				caCert = nil
			}
		}
	}

	if caKey == nil || caCert == nil {
		// Generate CA private key for webhook
		caKey, err = rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			return fmt.Errorf("failed to generate CA private key: %w", err)
		}

		// Create CA certificate template
		caCertTemplate := &x509.Certificate{
			SerialNumber: big.NewInt(time.Now().Unix()),
			Subject: pkix.Name{
				CommonName:   "webhook-ca",
				Organization: []string{"Rancher Desktop Daemon Webhook"},
			},
			NotBefore:             time.Now(),
			NotAfter:              time.Now().AddDate(10, 0, 0), // Valid for 10 years
			IsCA:                  true,
			ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
			KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
			BasicConstraintsValid: true,
		}

		// Generate self-signed CA certificate
		caCertDER, err := x509.CreateCertificate(
			rand.Reader,
			caCertTemplate,
			caCertTemplate, // Self-signed
			&caKey.PublicKey,
			caKey,
		)
		if err != nil {
			return fmt.Errorf("failed to create CA certificate: %w", err)
		}

		// Parse CA certificate for signing
		caCert, err = x509.ParseCertificate(caCertDER)
		if err != nil {
			return fmt.Errorf("failed to parse CA certificate: %w", err)
		}
	}

	// Generate webhook server private key
	webhookKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fmt.Errorf("failed to generate webhook private key: %w", err)
	}

	// Create webhook server certificate template with all service DNS names
	webhookCertTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().Unix() + 1),
		Subject: pkix.Name{
			CommonName:   "webhook-server",
			Organization: []string{"Rancher Desktop Daemon Webhook"},
		},
		DNSNames: cm.buildServiceDNSNames(),
		IPAddresses: []net.IP{
			net.IPv4(127, 0, 0, 1), // IPv4 localhost
			net.IPv6loopback,       // IPv6 localhost
		},
		NotBefore:   time.Now(),
		NotAfter:    time.Now().AddDate(1, 0, 0), // Valid for 1 year
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
	}

	// Add server IP if provided and valid
	if cm.serverIP != "" && cm.serverIP != "localhost" {
		if ip := net.ParseIP(cm.serverIP); ip != nil {
			webhookCertTemplate.IPAddresses = append(webhookCertTemplate.IPAddresses, ip)
			klog.V(2).Infof("Added server IP %s to webhook certificate SANs", cm.serverIP)
		}
	}

	// Generate webhook server certificate signed by CA
	webhookCertDER, err := x509.CreateCertificate(
		rand.Reader,
		webhookCertTemplate,
		caCert,
		&webhookKey.PublicKey,
		caKey,
	)
	if err != nil {
		return fmt.Errorf("failed to create webhook certificate: %w", err)
	}

	// Save webhook certificate
	webhookCertPath := filepath.Join(cm.certDir, cm.certName)
	if err := cm.saveCertificate(webhookCertPath, webhookCertDER); err != nil {
		return fmt.Errorf("failed to save webhook certificate: %w", err)
	}

	// Save webhook private key
	webhookKeyPath := filepath.Join(cm.certDir, cm.keyName)
	if err := cm.savePrivateKey(webhookKeyPath, webhookKey); err != nil {
		return fmt.Errorf("failed to save webhook private key: %w", err)
	}

	// Save CA certificate for webhook configuration
	webhookCACertPath := filepath.Join(cm.certDir, DefaultWebhookCACertFileName)
	if err := cm.saveCertificate(webhookCACertPath, caCert.Raw); err != nil {
		return fmt.Errorf("failed to save webhook CA certificate: %w", err)
	}

	// Save CA certificate private key for use by external controllers
	webhookCAKeyPath := filepath.Join(cm.certDir, defaultWebhookCAKeyFileName)
	if err := cm.savePrivateKey(webhookCAKeyPath, caKey); err != nil {
		return fmt.Errorf("failed to save webhook CA private key: %w", err)
	}

	klog.V(2).Infof("Shared webhook certificates generated successfully: cert=%s, key=%s, ca=%s",
		webhookCertPath, webhookKeyPath, webhookCACertPath)
	klog.V(2).Infof("Certificate includes DNS names: %v", webhookCertTemplate.DNSNames)
	return nil
}

// buildServiceDNSNames constructs the complete list of DNS names for the certificate
// including all service variations and standard localhost names.
func (cm *SharedWebhookCertificateManager) buildServiceDNSNames() []string {
	dnsNames := []string{
		"localhost",
		"webhook-service",
		"webhook-service.default",
		"webhook-service.default.svc",
		"webhook-service.default.svc.cluster.local",
	}

	// Add all registered service names with their FQDN variations
	for _, serviceName := range cm.serviceNames {
		dnsNames = append(dnsNames,
			serviceName,
			serviceName+".default",
			serviceName+".default.svc",
			serviceName+".default.svc.cluster.local",
		)
	}

	return dnsNames
}

// GetCABundle returns the raw PEM-encoded CA certificate bundle for webhook configuration.
func (cm *SharedWebhookCertificateManager) GetCABundle() ([]byte, error) {
	webhookCACertPath := filepath.Join(cm.certDir, DefaultWebhookCACertFileName)

	// Read the webhook CA certificate file
	caCertPEM, err := os.ReadFile(webhookCACertPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read webhook CA certificate file %s: %w", webhookCACertPath, err)
	}

	// Validate that it's a proper PEM certificate
	block, _ := pem.Decode(caCertPEM)
	if block == nil {
		return nil, errors.New("failed to decode PEM block from webhook CA certificate file")
	}

	if block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("expected CERTIFICATE block, got %s", block.Type)
	}

	// Return raw PEM bytes for webhook configuration
	return caCertPEM, nil
}

// CertificatesExist checks if webhook certificates already exist and are valid.
func (cm *SharedWebhookCertificateManager) CertificatesExist() bool {
	certPath := filepath.Join(cm.certDir, cm.certName)
	keyPath := filepath.Join(cm.certDir, cm.keyName)

	// Check if both files exist
	if _, err := os.Stat(certPath); os.IsNotExist(err) {
		return false
	}
	if _, err := os.Stat(keyPath); os.IsNotExist(err) {
		return false
	}

	// Check if certificate is still valid (not expired)
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return false
	}

	block, _ := pem.Decode(certPEM)
	if block == nil {
		return false
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false
	}

	// Check if certificate expires within 30 days
	if time.Until(cert.NotAfter) < 30*24*time.Hour {
		klog.V(2).Info("Webhook certificate expires soon, will regenerate")
		return false
	}

	// Check if certificate includes all required DNS names
	if !cm.certificateIncludesAllServiceNames(cert) {
		klog.V(2).Info("Webhook certificate missing required DNS names, will regenerate")
		return false
	}

	return true
}

// certificateIncludesAllServiceNames verifies that the certificate includes all required service DNS names.
func (cm *SharedWebhookCertificateManager) certificateIncludesAllServiceNames(cert *x509.Certificate) bool {
	requiredNames := cm.buildServiceDNSNames()
	certDNSNames := make(map[string]bool)

	for _, name := range cert.DNSNames {
		certDNSNames[name] = true
	}

	for _, required := range requiredNames {
		if !certDNSNames[required] {
			klog.V(2).Infof("Certificate missing required DNS name: %s", required)
			return false
		}
	}

	return true
}

// GetWebhookCertPaths returns the file paths for webhook certificate and key.
func (cm *SharedWebhookCertificateManager) GetWebhookCertPaths() (certPath, keyPath string) {
	certPath = filepath.Join(cm.certDir, cm.certName)
	keyPath = filepath.Join(cm.certDir, cm.keyName)
	return certPath, keyPath
}

// AddServiceNames adds new service names to the certificate manager.
// This requires regeneration of certificates to include the new DNS names.
func (cm *SharedWebhookCertificateManager) AddServiceNames(newServiceNames []string) {
	cm.serviceNames = append(cm.serviceNames, newServiceNames...)
	klog.V(2).Infof("Added webhook service names: %v", newServiceNames)
}

// GetServiceNames returns the current list of service names.
func (cm *SharedWebhookCertificateManager) GetServiceNames() []string {
	return append([]string{}, cm.serviceNames...) // Return a copy
}

// saveCertificate saves a certificate to the specified file path.
func (cm *SharedWebhookCertificateManager) saveCertificate(path string, certDER []byte) error {
	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("failed to create certificate directory: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certDER,
	})

	return os.WriteFile(path, certPEM, 0o644)
}

// savePrivateKey saves a private key to the specified file path.
func (cm *SharedWebhookCertificateManager) savePrivateKey(path string, key *rsa.PrivateKey) error {
	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("failed to create key directory: %w", err)
	}

	keyDER := x509.MarshalPKCS1PrivateKey(key)
	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: keyDER,
	})

	return os.WriteFile(path, keyPEM, 0o600)
}
