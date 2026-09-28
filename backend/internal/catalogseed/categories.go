package catalogseed

// IsCategorySlug reports whether a slug belongs to the canonical catalog.
func IsCategorySlug(slug string) bool {
	for _, candidate := range categorySlugs {
		if candidate == slug {
			return true
		}
	}
	return false
}
