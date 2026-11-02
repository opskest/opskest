package check

import (
	"crypto/tls"

	"opskest/internal/model"
)

// certInfoFromConnState builds a model.CertInfo from the leaf certificate
// of a completed TLS handshake, or nil when there is none.
func certInfoFromConnState(cs *tls.ConnectionState) *model.CertInfo {
	if cs == nil || len(cs.PeerCertificates) == 0 {
		return nil
	}
	leaf := cs.PeerCertificates[0]
	return &model.CertInfo{
		NotAfter: leaf.NotAfter,
		Issuer:   leaf.Issuer.CommonName,
		Subject:  leaf.Subject.CommonName,
	}
}
