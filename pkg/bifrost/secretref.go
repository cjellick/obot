package bifrost

import "github.com/maximhq/bifrost/core/schemas"

// IsSecretReference reports whether the engine would read a JSON value from the host's
// environment or a vault, rather than take it as given, wherever it accepts a secret. It
// decodes the value with the engine's own decoder, so it can't disagree with the engine on
// spelling, such as key names in another case.
func IsSecretReference(raw []byte) bool {
	var v schemas.SecretVar
	if err := v.UnmarshalJSON(raw); err != nil {
		return false
	}
	return v.IsFromSecret()
}

// SecretValue returns the value the engine reads from a JSON value it accepts as a secret,
// such as a region, which may be a string or an object holding one.
func SecretValue(raw []byte) string {
	var v schemas.SecretVar
	if err := v.UnmarshalJSON(raw); err != nil {
		return ""
	}
	return v.GetValue()
}
