package config

import (
	"fmt"
	"os"
	"regexp"

	. "github.com/roelfdiedericks/goclaw/internal/logging"
)

// envVarPattern matches ${VAR_NAME} where VAR_NAME follows shell variable naming rules
var envVarPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// ExpandEnvVars expands ${VAR_NAME} placeholders in the input bytes with their
// corresponding environment variable values. Returns an error if any referenced
// environment variable is not set.
//
// This function is used for runtime config loading where secrets may be injected
// via environment variables. It should NOT be used for setup wizard/editor contexts
// where the literal ${VAR} placeholders must be preserved.
func ExpandEnvVars(data []byte) ([]byte, error) {
	var missingVars []string

	result := envVarPattern.ReplaceAllFunc(data, func(match []byte) []byte {
		// Extract variable name from ${VAR_NAME}
		varName := envVarPattern.FindSubmatch(match)[1]
		name := string(varName)

		value, exists := os.LookupEnv(name)
		if !exists {
			missingVars = append(missingVars, name)
			return match // Keep original for error reporting
		}

		L_debug("config: expanded env var", "var", name, "valueLength", len(value))
		return []byte(value)
	})

	if len(missingVars) > 0 {
		if len(missingVars) == 1 {
			return nil, fmt.Errorf("config: environment variable ${%s} is not set", missingVars[0])
		}
		return nil, fmt.Errorf("config: environment variables not set: %v", missingVars)
	}

	return result, nil
}

// HasEnvVars checks if the input contains any ${VAR_NAME} placeholders.
// Useful for logging or validation purposes.
func HasEnvVars(data []byte) bool {
	return envVarPattern.Match(data)
}
