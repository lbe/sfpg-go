// Package client provides an HTTP client for communicating with the sfpg-go server.
// It handles authentication via session cookies and fetches dashboard HTML for parsing.
//
// The client automatically manages:
//   - Session cookies via an embedded cookie jar
//   - CSRF protection by sending the Origin header with requests
//   - Error detection for unauthorized and network errors
//
// Example usage:
//
//	c := client.New("http://localhost:8083")
//	err := c.Login(ctx, "admin", "password")
//	if err != nil {
//	    // handle error
//	}
//	html, err := c.FetchDashboard(ctx)
//	if err != nil {
//	    // handle error
//	}
//	// parse html...
package client
