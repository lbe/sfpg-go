// Package template provides pure functions for building template data maps.
package template

// AddAuthToData adds authentication state to a template data map.
// If data is nil, a new map is created. Returns the modified map.
func AddAuthToData(data map[string]any, isAuthenticated bool) map[string]any {
	if data == nil {
		data = make(map[string]any)
	}
	data["IsAuthenticated"] = isAuthenticated
	return data
}

// AddCommonData adds authentication state to a template data map.
// If data is nil, a new map is created. Returns the modified map.
func AddCommonData(data map[string]any, isAuthenticated bool) map[string]any {
	return AddAuthToData(data, isAuthenticated)
}
