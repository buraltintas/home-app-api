// Package brand contains the canonical public identity shared by runtime
// surfaces. Stable repository, database, and migration identifiers live outside
// this package because they are infrastructure identities, not display names.
package brand

const (
	ProductName = "Boşa Gezme!"
	// Tagline completes the name rather than repeating it: set under the mark it reads
	// "Boşa Gezme! / Bize Sor.", which is the whole line. Slogan is the standalone form for
	// places with no mark beside it. Both stay Turkish in every locale -- the line is
	// wordplay on the product's own name, and a translation is not the same line.
	Tagline = "Bize Sor."
	Slogan  = "Boşa Gezme, Bize Sor."

	Domain           = "bosagezme.com"
	WebsiteURL       = "https://" + Domain
	DefaultEmailFrom = "no-reply@" + Domain
	JWTIssuer        = WebsiteURL
	JWTAudience      = "bosagezme-clients"
	ServiceName      = "bosagezme-api"
	TracerName       = Domain + "/api"
	MetricsNamespace = "bosagezme"
)
