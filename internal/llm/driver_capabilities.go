package llm

// DriverSupportsEmbeddings reports whether a provider driver can be used for the
// embeddings purpose. This is purpose-level capability, not whether a specific
// provider instance is currently available.
func DriverSupportsEmbeddings(driver string) bool {
	desc, ok := GetDriver(driver)
	return ok && desc.SupportsEmbeddings
}
