package greggpttools

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

type Arguments map[string]any

func parseArguments(schema JSONSchema, raw json.RawMessage) (Arguments, error) {
	if len(raw) == 0 || string(raw) == "null" {
		raw = []byte("{}")
	}
	var args Arguments
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("arguments must be a JSON object: %w", err)
	}
	if args == nil {
		args = Arguments{}
	}
	clampArguments(schema, args)
	if err := validateArguments(schema, args); err != nil {
		return nil, err
	}
	return args, nil
}

func clampArguments(schema JSONSchema, args Arguments) {
	for name, spec := range schema.Properties {
		if spec.Type != "integer" || !spec.ClampMaximum || spec.Maximum == nil {
			continue
		}
		value, ok := args[name]
		if !ok || value == nil {
			continue
		}
		n, ok := intValue(value)
		if ok && n > *spec.Maximum {
			args[name] = *spec.Maximum
		}
	}
}

func validateArguments(schema JSONSchema, args Arguments) error {
	if schema.Type != "object" {
		return fmt.Errorf("tool schema must be object")
	}
	for name := range args {
		if _, ok := schema.Properties[name]; !ok && !schema.AdditionalProperties {
			return fmt.Errorf("unknown argument %q", name)
		}
	}
	for _, name := range schema.Required {
		if _, ok := args[name]; !ok {
			return fmt.Errorf("missing required argument %q", name)
		}
	}
	for name, spec := range schema.Properties {
		value, ok := args[name]
		if !ok || value == nil {
			continue
		}
		if err := validateValue(name, spec, value); err != nil {
			return err
		}
	}
	return nil
}

func validateValue(name string, spec ParamSpec, value any) error {
	switch spec.Type {
	case "string":
		s, ok := value.(string)
		if !ok {
			return fmt.Errorf("argument %q must be a string", name)
		}
		if spec.MinLength > 0 && len(strings.TrimSpace(s)) < spec.MinLength {
			return fmt.Errorf("argument %q must not be empty", name)
		}
		if len(spec.Enum) > 0 && !containsEnum(spec.Enum, s) {
			return fmt.Errorf("argument %q must be one of: %s", name, enumList(spec.Enum))
		}
	case "integer":
		n, ok := intValue(value)
		if !ok {
			return fmt.Errorf("argument %q must be an integer", name)
		}
		if spec.Minimum != nil && n < *spec.Minimum {
			return fmt.Errorf("argument %q must be >= %d", name, *spec.Minimum)
		}
		if spec.Maximum != nil && n > *spec.Maximum {
			return fmt.Errorf("argument %q must be <= %d", name, *spec.Maximum)
		}
		if len(spec.Enum) > 0 && !containsEnum(spec.Enum, strconv.Itoa(n)) {
			return fmt.Errorf("argument %q must be one of: %s", name, enumList(spec.Enum))
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("argument %q must be a boolean", name)
		}
	case "array":
		values, ok := value.([]any)
		if !ok {
			return fmt.Errorf("argument %q must be an array", name)
		}
		if spec.MinItems > 0 && len(values) < spec.MinItems {
			return fmt.Errorf("argument %q must include at least %d item(s)", name, spec.MinItems)
		}
		if spec.Items != nil {
			for i, item := range values {
				if err := validateValue(fmt.Sprintf("%s[%d]", name, i), *spec.Items, item); err != nil {
					return err
				}
			}
		}
	default:
		return fmt.Errorf("unsupported schema type %q for argument %q", spec.Type, name)
	}
	return nil
}

func stringArg(args Arguments, name string) string {
	value, _ := args[name].(string)
	return strings.TrimSpace(value)
}

func boolArg(args Arguments, name string) bool {
	value, _ := args[name].(bool)
	return value
}

func intArg(args Arguments, name string, fallback int) int {
	value, ok := intValue(args[name])
	if !ok {
		return fallback
	}
	return value
}

func stringSliceArg(args Arguments, name string) []string {
	raw, ok := args[name].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}

func intValue(value any) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		n := int(v)
		return n, float64(n) == v
	case json.Number:
		n, err := v.Int64()
		return int(n), err == nil
	default:
		return 0, false
	}
}

func containsEnum(values []any, value string) bool {
	for _, candidate := range values {
		if fmt.Sprint(candidate) == value {
			return true
		}
	}
	return false
}

func enumList(values []any) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, fmt.Sprint(value))
	}
	return strings.Join(parts, ", ")
}

func intPtr(v int) *int {
	return &v
}
