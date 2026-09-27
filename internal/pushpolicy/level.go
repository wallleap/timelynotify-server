package pushpolicy

import "strings"

// HarmonyPassive reports whether Harmony delivery should be history-only.
// Other Bark levels have no special Huawei V3 entitlement and use the normal alert.
func HarmonyPassive(extras map[string]interface{}) bool {
	value, ok := lookupFold(extras, "level")
	if !ok {
		return false
	}
	level, ok := value.(string)
	return ok && strings.EqualFold(strings.TrimSpace(level), "passive")
}
