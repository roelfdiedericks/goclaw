// Package forms provides form definitions for config editing.
// FormDef structures can be rendered to TUI (huh) or web forms.
package forms

// FieldType defines the type of form field
type FieldType int

const (
	Toggle   FieldType = iota // Boolean on/off
	Text                      // Single-line text input
	Number                    // Numeric input (int or float)
	Secret                    // Password/token input (masked)
	Select                    // Dropdown selection
	TextArea                  // Multi-line text input
)

// FormDef defines a form for editing a config struct
type FormDef struct {
	Title       string      // Form title
	Description string      // Optional description
	Sections    []Section   // Form sections (groups of fields)
	Actions     []ActionDef // Available actions (Test, Apply, etc.)
}

// Section groups related fields
type Section struct {
	Title     string   // Section title
	Desc      string   // Optional description
	Fields    []Field  // Fields in this section
	Collapsed bool     // Start collapsed in UI
	Nested    *FormDef // For nested config structs
	FieldName string   // Struct field name for nested sections (e.g., "Query")
}

// Field defines a single form field
type Field struct {
	Name     string    // JSON field name (maps to struct field)
	Title    string    // Display title
	Desc     string    // Help text/description
	Type     FieldType // Field type
	Default  any       // Default value
	Required bool      // Whether field is required

	// Numeric constraints (for Number type)
	Min  float64 // Minimum value
	Max  float64 // Maximum value
	Step float64 // Step increment (0 = any)

	// Select options (for Select type)
	Options []Option
}

// Option is a choice for Select fields
type Option struct {
	Label string // Display text
	Value string // Actual value
}

// ActionDef defines an action button on the form
type ActionDef struct {
	Name    string // Action name (used with action bus)
	Label   string // Button label
	Desc    string // Tooltip/description
	Confirm string // Confirmation prompt (empty = no confirm)
}

// Configurable is implemented by config structs that provide form definitions
type Configurable interface {
	FormDef() FormDef
}

// Validatable is implemented by config structs that can self-validate
type Validatable interface {
	Validate() error
}

// Defaultable is implemented by config structs that provide defaults
type Defaultable interface {
	Defaults() any
}
