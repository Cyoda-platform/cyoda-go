package common

import "strings"

// TagsOverlap checks if any required tag (comma-separated) matches any tag in the slice.
// If requiredCSV is empty, returns true (any member matches).
func TagsOverlap(tags []string, requiredCSV string) bool {
	if requiredCSV == "" {
		return true
	}
	for _, req := range strings.Split(requiredCSV, ",") {
		req = strings.TrimSpace(req)
		for _, t := range tags {
			if req == t {
				return true
			}
		}
	}
	return false
}
