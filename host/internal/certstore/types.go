package certstore

// CertInfo is the JSON shape returned to the caller for each certificate.
// Field names match what the Chrome extension / VaultSign frontend expects.
type CertInfo struct {
	Thumbprint    string   `json:"thumbprint"`              // SHA-1 of DER, hex uppercase
	Subject       string   `json:"subject"`                 // RFC 2253 distinguished name
	Issuer        string   `json:"issuer"`                  // RFC 2253 distinguished name
	SerialNumber  string   `json:"serialNumber"`            // hex lowercase
	NotBefore     string   `json:"notBefore"`               // RFC 3339
	NotAfter      string   `json:"notAfter"`                // RFC 3339
	CN            string   `json:"cn"`                      // subject common name
	CPF           string   `json:"cpf,omitempty"`           // ICP-Brasil PF (otherName 2.16.76.1.3.1)
	CNPJ          string   `json:"cnpj,omitempty"`          // ICP-Brasil PJ (otherName 2.16.76.1.3.3)
	HasPrivateKey bool     `json:"hasPrivateKey"`
	KeyUsage      []string `json:"keyUsage"`
}
