package gh

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

// GraphQL runs a query and decodes its "data" field into out. When GitHub
// answers with data and errors (such as repos in an organisation your token
// isn't authorised for), the data is decoded and a *PartialError returned.
func (c *Client) GraphQL(ctx context.Context, query string, vars map[string]any, out any) error {
	endpoint := c.apiBase + "/graphql"
	if base, ok := strings.CutSuffix(c.apiBase, "/api/v3"); ok {
		endpoint = base + "/api/graphql" // GitHub Enterprise Server
	}
	var resp struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"errors"`
	}
	// A query reads only, so it is tried again when GitHub fails on it; a mutation is sent once.
	isQuery := !strings.HasPrefix(strings.TrimSpace(query), "mutation")
	if err := c.sendJSON(ctx, http.MethodPost, endpoint, map[string]any{"query": query, "variables": vars}, &resp, false, isQuery); err != nil {
		return err
	}
	hasData := len(resp.Data) > 0 && string(resp.Data) != "null"
	if hasData {
		if err := json.Unmarshal(resp.Data, out); err != nil {
			return err
		}
	}
	if len(resp.Errors) == 0 {
		return nil
	}
	var msgs []string
	forbidden := false
	for _, e := range resp.Errors {
		msgs = append(msgs, e.Message)
		if e.Type == "FORBIDDEN" || strings.Contains(e.Message, "Resource not accessible by") {
			forbidden = true
		}
	}
	if hasData {
		return &PartialError{msgs}
	}
	if forbidden {
		return c.forbiddenToAccess(ctx, strings.Join(msgs, "; "))
	}
	return errors.New("GitHub GraphQL: " + strings.Join(msgs, "; "))
}

// PartialError is a GraphQL answer that came with data for part of what was asked.
type PartialError struct{ Messages []string }

func (e *PartialError) Error() string {
	return "GitHub GraphQL, partly answered: " + strings.Join(e.Messages, "; ")
}

// allowPartial treats a partial GraphQL answer as an answer.
func allowPartial(err error) error {
	var p *PartialError
	if errors.As(err, &p) {
		return nil
	}
	return err
}
