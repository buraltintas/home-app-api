// Package brand contains the canonical public identity shared by runtime
// surfaces. Stable repository, database, and migration identifiers live outside
// this package because they are infrastructure identities, not display names.
package brand

const (
	ProductName      = "Boşa Gezme!"
	Domain           = "bosagezme.com"
	WebsiteURL       = "https://" + Domain
	DefaultEmailFrom = "no-reply@" + Domain
	JWTIssuer        = WebsiteURL
	JWTAudience      = "bosagezme-clients"
	ServiceName      = "bosagezme-api"
	TracerName       = Domain + "/api"
	MetricsNamespace = "bosagezme"
)
