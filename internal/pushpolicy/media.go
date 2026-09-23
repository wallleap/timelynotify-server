package pushpolicy

import "strings"

// HarmonyImageURL returns the URL mapped to Huawei notification.image.
// Bark icon has priority; Bark image is used only when icon is absent.
func HarmonyImageURL(extras map[string]interface{}) string {
	for _, key := range []string{"icon", "image"} {
		value, ok := lookupFold(extras, key)
		if !ok {
			continue
		}
		url, isString := value.(string)
		if isString {
			url = strings.TrimSpace(strings.Trim(strings.TrimSpace(url), "`"))
			if url != "" {
				return url
			}
		}
	}
	return ""
}
