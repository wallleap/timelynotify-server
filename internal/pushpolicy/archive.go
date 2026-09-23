// Package pushpolicy contains delivery-independent policy decisions for push requests.
package pushpolicy

import (
	"fmt"
	"strings"
)

// ShouldPublishHistory reports whether a logical push should be written to the
// remote history store. Encrypted Harmony messages are retained temporarily so
// the client can fetch and decrypt them even when the sender disables archiving.
func ShouldPublishHistory(extras map[string]interface{}, hasHarmonyTarget bool) bool {
	archiveValue, archiveSpecified := lookupFold(extras, "isarchive")
	if !archiveSpecified || isEnabled(archiveValue) {
		return true
	}

	ciphertext, hasCiphertext := lookupFold(extras, "ciphertext")
	ciphertextString, isString := ciphertext.(string)
	return hasHarmonyTarget && hasCiphertext && isString && strings.TrimSpace(ciphertextString) != ""
}

func lookupFold(values map[string]interface{}, key string) (interface{}, bool) {
	for currentKey, value := range values {
		if strings.EqualFold(currentKey, key) {
			return value, true
		}
	}
	return nil, false
}

func isEnabled(value interface{}) bool {
	if enabled, ok := value.(bool); ok {
		return enabled
	}
	return strings.TrimSpace(fmt.Sprint(value)) == "1"
}
