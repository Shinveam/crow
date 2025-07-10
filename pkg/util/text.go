package util

import "regexp"

// RemoveAllPunctuation 移除所有标点符号
func RemoveAllPunctuation(text string) string {
	re := regexp.MustCompile(`[\p{P}\p{S}]+`)
	removed := re.ReplaceAllString(text, "")
	return removed
}
