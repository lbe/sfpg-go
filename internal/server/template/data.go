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

// AddCSRFToData adds CSRF token to a template data map.
// If data is nil, a new map is created. Returns the modified map.
func AddCSRFToData(data map[string]any, csrfToken string) map[string]any {
	if data == nil {
		data = make(map[string]any)
	}
	data["CSRFToken"] = csrfToken
	return data
}

// AddCommonData adds both authentication state and CSRF token to a template data map.
// If data is nil, a new map is created. Returns the modified map.
func AddCommonData(data map[string]any, isAuthenticated bool, csrfToken string) map[string]any {
	data = AddAuthToData(data, isAuthenticated)
	data = AddCSRFToData(data, csrfToken)
	return data
}
