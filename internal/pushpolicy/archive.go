// Package pushpolicy contains delivery-independent policy decisions for push requests.
package pushpolicy

import (
	"fmt"
	"strings"
)

const (
	archivedEncryptedTitle  = "[订阅] 加密通知"
	archivedEncryptedBody   = "请打开及时通知查看加密内容"
	transientEncryptedTitle = "[订阅] 加密即时通知"
	transientEncryptedBody  = "请打开及时通知解密并查看，此通知不会保存到历史记录"
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

// EncryptedPlaceholder returns safe notification-bar copy without exposing
// ciphertext. Archived messages retain the existing copy for compatibility;
// transient messages explain that opening the app is required to view them.
func EncryptedPlaceholder(extras map[string]interface{}) (string, string) {
	archiveValue, archiveSpecified := lookupFold(extras, "isarchive")
	if archiveSpecified && !isEnabled(archiveValue) {
		return transientEncryptedTitle, transientEncryptedBody
	}
	return archivedEncryptedTitle, archivedEncryptedBody
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
