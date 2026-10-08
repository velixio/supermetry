// Package csvsafe protects exported CSV cells from spreadsheet formula
// injection. Tracked values such as paths and referrers are visitor-controlled.
package csvsafe

// Cell prefixes a single quote to text that a spreadsheet would otherwise
// evaluate as a formula (leading =, +, -, @, tab or carriage return).
func Cell(value string) string {
	if value == "" {
		return value
	}
	switch value[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + value
	}
	return value
}
