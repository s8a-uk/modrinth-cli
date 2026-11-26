package commands

// TagKindFields returns the fields of a tag list kind (nil for arrays of strings).
func TagKindFields(kind string) []string {
	k, _ := findTagKind(kind)
	return k.fields
}
