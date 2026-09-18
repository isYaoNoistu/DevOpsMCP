package targets

type TLS struct {
	CAPEM      string `json:"ca_pem,omitempty"`
	CertPEM    string `json:"cert_pem,omitempty"`
	KeyPEM     string `json:"key_pem,omitempty"`
	Enabled    bool   `json:"enabled"`
	CAFile     string `json:"ca_file,omitempty"`
	CertFile   string `json:"cert_file,omitempty"`
	KeyFile    string `json:"key_file,omitempty"`
	ServerName string `json:"server_name,omitempty"`
}
type SASL struct {
	Mechanism string `json:"mechanism,omitempty"`
	Username  string `json:"username,omitempty"`
	Password  string `json:"password,omitempty"`
}
type Target struct {
	Name         string            `json:"name"`
	Brokers      []string          `json:"brokers"`
	Description  string            `json:"description,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
	TLS          TLS               `json:"tls,omitempty"`
	SASL         SASL              `json:"sasl,omitempty"`
	Topics       []string          `json:"topics"`
	Groups       []string          `json:"groups"`
	AllowPayload bool              `json:"allow_payload,omitempty"`
}
