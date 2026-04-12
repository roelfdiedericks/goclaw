package llm

func init() {
	RegisterDriver(DriverDescriptor{
		ID:                 "llamacpp",
		Label:              "Llama.cpp",
		Order:              35,
		IsLocal:            true,
		SupportsEmbeddings: true,
		New: func(name string, cfg LLMProviderConfig) (Provider, error) {
			return NewLlamaCppProvider(name, cfg)
		},
	})
}
