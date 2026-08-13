package contract

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestOpenAPIContainsOnlyBusinessV1Paths(t *testing.T) {
	document := loadOpenAPIDocument(t)
	paths := object(t, document, "paths")
	for path := range paths {
		if !strings.HasPrefix(path, "/v1/") {
			t.Errorf("public OpenAPI contains non-business path %q", path)
		}
	}
	for _, path := range []string{"/health/live", "/health/ready", "/metrics"} {
		if _, ok := paths[path]; ok {
			t.Errorf("public OpenAPI contains operational path %q", path)
		}
	}
	for _, rawTag := range document["tags"].([]any) {
		tag, ok := rawTag.(map[string]any)
		if ok && tag["name"] == "Operations" {
			t.Error("public OpenAPI contains Operations tag")
		}
	}
	components := object(t, document, "components")
	if _, ok := object(t, components, "schemas")["Health"]; ok {
		t.Error("public OpenAPI contains Health schema")
	}
	if _, ok := object(t, components, "responses")["DependencyUnavailable"]; ok {
		t.Error("public OpenAPI contains DependencyUnavailable response")
	}
}

func TestTask2OpenAPIContract(t *testing.T) {
	document := loadOpenAPIDocument(t)
	components := object(t, document, "components")
	schemas := object(t, components, "schemas")
	paths := object(t, document, "paths")

	replayPath := object(t, paths, "/v1/notifications/{notification_id}/replay")
	replayPost := object(t, replayPath, "post")
	requestBody := object(t, replayPost, "requestBody")
	if required, _ := requestBody["required"].(bool); !required {
		t.Fatal("replay request body is not required")
	}
	content := object(t, requestBody, "content")
	jsonContent := object(t, content, "application/json")
	requestSchema := object(t, jsonContent, "schema")
	if requestSchema["$ref"] != "#/components/schemas/ReplayNotificationRequest" {
		t.Fatalf("replay request schema=%v", requestSchema)
	}
	replayRequest := object(t, schemas, "ReplayNotificationRequest")
	if additional, ok := replayRequest["additionalProperties"].(bool); !ok || additional {
		t.Fatalf("additionalProperties=%v", replayRequest["additionalProperties"])
	}
	if !stringSliceContains(replayRequest["required"], "reason") {
		t.Fatalf("required=%v", replayRequest["required"])
	}
	reason := object(t, object(t, replayRequest, "properties"), "reason")
	if reason["minLength"] != float64(1) || reason["maxLength"] != float64(1000) {
		t.Fatalf("reason=%v", reason)
	}
	if _, ok := object(t, replayPost, "responses")["415"]; !ok {
		t.Fatal("replay response 415 is missing")
	}

	failureCode := object(t, schemas, "NotificationFailureCode")
	wantCodes := []string{
		"NOTIFICATION_PAYLOAD_INVALID", "SMTP_CONFIGURATION_ERROR", "SMTP_CONNECTION_FAILED", "SMTP_TIMEOUT",
		"SMTP_TLS_FAILED", "SMTP_AUTHENTICATION_FAILED", "SMTP_RECIPIENT_REJECTED", "SMTP_MESSAGE_REJECTED",
		"SMTP_TEMPORARY_FAILURE", "SMTP_PROTOCOL_ERROR", "DELIVERY_FAILED", "LEASE_EXPIRED", "RETRY_LIMIT_EXHAUSTED",
	}
	if got := stringSlice(failureCode["enum"]); !equalStrings(got, wantCodes) {
		t.Fatalf("failure enum=%v", got)
	}
	notificationProperties := object(t, object(t, schemas, "Notification"), "properties")
	for _, field := range []string{"last_error_code", "last_error", "replays"} {
		if _, ok := notificationProperties[field]; !ok {
			t.Fatalf("Notification.%s is missing", field)
		}
	}
	if _, ok := notificationProperties["lease_token"]; ok {
		t.Fatal("Notification exposes lease_token")
	}
	attemptProperties := object(t, object(t, schemas, "NotificationAttempt"), "properties")
	if _, ok := attemptProperties["error_code"]; !ok {
		t.Fatal("NotificationAttempt.error_code is missing")
	}
	replayEvent := object(t, schemas, "NotificationReplayEvent")
	for _, field := range []string{"sequence", "delivery_cycle", "actor_id", "request_id", "channel", "reason", "occurred_at"} {
		if _, ok := object(t, replayEvent, "properties")[field]; !ok {
			t.Fatalf("NotificationReplayEvent.%s is missing", field)
		}
	}

	assignment := object(t, object(t, paths, "/v1/work-items/{work_item_id}/assignment"), "put")
	assignmentOK := object(t, object(t, assignment, "responses"), "200")
	if err := validateRequiredHeader(document, assignmentOK, "Idempotent-Replayed"); err != nil {
		t.Fatal(err)
	}

	for path, rawPath := range paths {
		pathItem, ok := rawPath.(map[string]any)
		if !ok {
			continue
		}
		for method, rawOperation := range pathItem {
			if method != "get" && method != "post" && method != "put" && method != "delete" && method != "patch" {
				continue
			}
			operation := rawOperation.(map[string]any)
			responses := object(t, operation, "responses")
			for status, rawResponse := range responses {
				if len(status) > 0 && status[0] == '2' {
					response := rawResponse.(map[string]any)
					if err := validateRequiredHeader(document, response, "X-Request-ID"); err != nil {
						t.Errorf("%s %s %s: %v", method, path, status, err)
					}
				}
			}
		}
	}
	for name, rawResponse := range object(t, components, "responses") {
		if err := validateRequiredHeader(document, rawResponse.(map[string]any), "X-Request-ID"); err != nil {
			t.Errorf("component response %s: %v", name, err)
		}
	}
}

func loadOpenAPIDocument(t *testing.T) map[string]any {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve contract path")
	}
	content, err := os.ReadFile(filepath.Join(filepath.Dir(file), "../../api/openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err = json.Unmarshal(content, &document); err != nil {
		t.Fatal(err)
	}
	return document
}

func object(t *testing.T, source map[string]any, key string) map[string]any {
	t.Helper()
	value, ok := source[key].(map[string]any)
	if !ok {
		t.Fatalf("%s is not an object: %T", key, source[key])
	}
	return value
}

func stringSlice(value any) []string {
	items, _ := value.([]any)
	result := make([]string, 0, len(items))
	for _, item := range items {
		text, _ := item.(string)
		result = append(result, text)
	}
	return result
}

func stringSliceContains(value any, want string) bool {
	for _, item := range stringSlice(value) {
		if item == want {
			return true
		}
	}
	return false
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func TestOpenAPIContract(t *testing.T) {
	document := loadOpenAPIDocument(t)
	if document["openapi"] != "3.1.0" {
		t.Fatalf("openapi=%v", document["openapi"])
	}

	wantResponses := map[string]map[string][]string{
		"/v1/work-items": {
			"get":  {"200", "400", "401", "403", "500", "503"},
			"post": {"200", "201", "400", "401", "403", "409", "415", "500", "503"},
		},
		"/v1/me/work-items": {"get": {"200", "400", "401", "403", "500", "503"}},
		"/v1/work-items/{work_item_id}": {
			"get": {"200", "400", "401", "403", "404", "500", "503"},
		},
		"/v1/work-items/{work_item_id}/assignment": {
			"put": {"200", "400", "401", "403", "404", "409", "415", "422", "500", "503"},
		},
		"/v1/work-items/{work_item_id}/start": {
			"post": {"200", "400", "401", "403", "404", "409", "500", "503"},
		},
		"/v1/work-items/{work_item_id}/complete": {
			"post": {"200", "400", "401", "403", "404", "409", "500", "503"},
		},
		"/v1/work-items/{work_item_id}/history": {
			"get": {"200", "400", "401", "403", "404", "500", "503"},
		},
		"/v1/notifications": {
			"get": {"200", "400", "401", "403", "500", "503"},
		},
		"/v1/notifications/{notification_id}": {
			"get": {"200", "400", "401", "403", "404", "500", "503"},
		},
		"/v1/notifications/{notification_id}/replay": {
			"post": {"202", "400", "401", "403", "404", "409", "415", "500", "503"},
		},
	}
	paths := object(t, document, "paths")
	if !sameStringSet(mapKeys(paths), mapKeys(wantResponses)) {
		t.Fatalf("public paths=%v want=%v", mapKeys(paths), mapKeys(wantResponses))
	}
	operationIDs := map[string]string{}
	for path, methods := range wantResponses {
		pathItem := object(t, paths, path)
		if got := operationKeys(pathItem); !sameStringSet(got, mapKeys(methods)) {
			t.Errorf("%s methods=%v want=%v", path, got, mapKeys(methods))
		}
		for method, statuses := range methods {
			operation := object(t, pathItem, method)
			operationID, _ := operation["operationId"].(string)
			if strings.TrimSpace(operationID) == "" {
				t.Errorf("%s %s has blank operationId", strings.ToUpper(method), path)
			} else if previous, exists := operationIDs[operationID]; exists {
				t.Errorf("operationId %q is reused by %s and %s %s", operationID, previous, strings.ToUpper(method), path)
			} else {
				operationIDs[operationID] = strings.ToUpper(method) + " " + path
			}
			if _, overridden := operation["security"]; overridden {
				t.Errorf("%s %s overrides top-level security", strings.ToUpper(method), path)
			}
			responses := object(t, operation, "responses")
			if got := mapKeys(responses); !sameStringSet(got, statuses) {
				t.Errorf("%s %s responses=%v want=%v", strings.ToUpper(method), path, got, statuses)
			}
			for status, rawResponse := range responses {
				response := resolveResponse(t, document, rawResponse)
				if err := validateRequiredHeader(document, response, "X-Request-ID"); err != nil {
					t.Errorf("%s %s %s: %v", strings.ToUpper(method), path, status, err)
				}
				if status == "401" {
					if err := validateRequiredHeader(document, response, "WWW-Authenticate"); err != nil {
						t.Errorf("%s %s 401: %v", strings.ToUpper(method), path, err)
					}
				}
				if len(status) == 3 && (status[0] == '4' || status[0] == '5') {
					content := object(t, response, "content")
					media := object(t, content, "application/json")
					schema := object(t, media, "schema")
					if schema["$ref"] != "#/components/schemas/ErrorEnvelope" {
						t.Errorf("%s %s %s error schema=%v", strings.ToUpper(method), path, status, schema)
					}
				}
			}
		}
	}

	topSecurity, ok := document["security"].([]any)
	if !ok || len(topSecurity) != 1 {
		t.Fatalf("security=%v", document["security"])
	}
	securityEntry, ok := topSecurity[0].(map[string]any)
	if !ok || len(securityEntry) != 1 || securityEntry["bearerAuth"] == nil || len(securityEntry["bearerAuth"].([]any)) != 0 {
		t.Fatalf("security=%v", document["security"])
	}
	components := object(t, document, "components")
	bearer := object(t, object(t, components, "securitySchemes"), "bearerAuth")
	if bearer["type"] != "http" || bearer["scheme"] != "bearer" {
		t.Fatalf("bearerAuth=%v", bearer)
	}

	schemas := object(t, components, "schemas")
	assertClosedSchema(t, object(t, schemas, "ErrorEnvelope"), []string{"error"}, []string{"error"})
	assertClosedSchema(t, object(t, schemas, "Error"), []string{"code", "details", "message", "request_id"}, []string{"code", "message", "request_id"})

	for _, required := range []struct{ path, method, status, header string }{
		{path: "/v1/work-items", method: "post", status: "201", header: "ETag"},
		{path: "/v1/work-items", method: "post", status: "200", header: "ETag"},
		{path: "/v1/work-items", method: "post", status: "200", header: "Idempotent-Replayed"},
		{path: "/v1/work-items/{work_item_id}", method: "get", status: "200", header: "ETag"},
		{path: "/v1/work-items/{work_item_id}/assignment", method: "put", status: "200", header: "ETag"},
		{path: "/v1/work-items/{work_item_id}/assignment", method: "put", status: "200", header: "Idempotent-Replayed"},
		{path: "/v1/work-items/{work_item_id}/start", method: "post", status: "200", header: "ETag"},
		{path: "/v1/work-items/{work_item_id}/start", method: "post", status: "200", header: "Idempotent-Replayed"},
		{path: "/v1/work-items/{work_item_id}/complete", method: "post", status: "200", header: "ETag"},
		{path: "/v1/work-items/{work_item_id}/complete", method: "post", status: "200", header: "Idempotent-Replayed"},
	} {
		rawResponse := object(t, object(t, object(t, object(t, paths, required.path), required.method), "responses"), required.status)
		response := resolveResponse(t, document, rawResponse)
		if err := validateRequiredHeader(document, response, required.header); err != nil {
			t.Errorf("%s %s %s: %v", strings.ToUpper(required.method), required.path, required.status, err)
		}
	}
}

func TestOpenAPIRequiredHeadersFailClosed(t *testing.T) {
	document := loadOpenAPIDocument(t)
	responses := object(t, object(t, document, "components"), "responses")
	invalidInput := object(t, responses, "InvalidInput")
	authenticationRequired := object(t, responses, "AuthenticationRequired")
	paths := object(t, document, "paths")
	created := object(t, object(t, object(t, object(t, paths, "/v1/work-items"), "post"), "responses"), "201")
	replayed := object(t, object(t, object(t, object(t, paths, "/v1/work-items"), "post"), "responses"), "200")

	for _, check := range []struct {
		name     string
		response map[string]any
		header   string
	}{
		{name: "request id", response: invalidInput, header: "X-Request-ID"},
		{name: "authentication challenge", response: authenticationRequired, header: "WWW-Authenticate"},
		{name: "entity tag", response: created, header: "ETag"},
		{name: "idempotency replay", response: replayed, header: "Idempotent-Replayed"},
	} {
		t.Run(check.name, func(t *testing.T) {
			if err := validateRequiredHeader(document, check.response, check.header); err != nil {
				t.Fatal(err)
			}
		})
	}

	tests := []struct {
		name, header, want string
		mutate             func(map[string]any) map[string]any
	}{
		{
			name: "dangling local ref", header: "X-Request-ID", want: "missing segment",
			mutate: func(document map[string]any) map[string]any {
				response := object(t, object(t, object(t, document, "components"), "responses"), "InvalidInput")
				object(t, response, "headers")["X-Request-ID"] = map[string]any{"$ref": "#/components/headers/DOES_NOT_EXIST"}
				return response
			},
		},
		{
			name: "cyclic local refs", header: "X-Request-ID", want: "cyclic header ref",
			mutate: func(document map[string]any) map[string]any {
				components := object(t, document, "components")
				headers := object(t, components, "headers")
				headers["LoopA"] = map[string]any{"$ref": "#/components/headers/LoopB"}
				headers["LoopB"] = map[string]any{"$ref": "#/components/headers/LoopA"}
				response := object(t, object(t, components, "responses"), "InvalidInput")
				object(t, response, "headers")["X-Request-ID"] = map[string]any{"$ref": "#/components/headers/LoopA"}
				return response
			},
		},
		{
			name: "nonlocal ref", header: "X-Request-ID", want: "unsupported non-local header ref",
			mutate: func(document map[string]any) map[string]any {
				response := object(t, object(t, object(t, document, "components"), "responses"), "InvalidInput")
				object(t, response, "headers")["X-Request-ID"] = map[string]any{"$ref": "https://example.test/header.json"}
				return response
			},
		},
		{
			name: "malformed local ref", header: "X-Request-ID", want: "malformed header ref",
			mutate: func(document map[string]any) map[string]any {
				response := object(t, object(t, object(t, document, "components"), "responses"), "InvalidInput")
				object(t, response, "headers")["X-Request-ID"] = map[string]any{"$ref": "#components/headers/XRequestID"}
				return response
			},
		},
		{
			name: "request id semantics", header: "X-Request-ID", want: "X-Request-ID schema",
			mutate: func(document map[string]any) map[string]any {
				components := object(t, document, "components")
				object(t, object(t, object(t, components, "headers"), "XRequestID"), "schema")["maxLength"] = float64(129)
				return object(t, object(t, components, "responses"), "InvalidInput")
			},
		},
		{
			name: "authentication semantics", header: "WWW-Authenticate", want: "WWW-Authenticate schema",
			mutate: func(document map[string]any) map[string]any {
				components := object(t, document, "components")
				object(t, object(t, object(t, components, "headers"), "WWWAuthenticate"), "schema")["enum"] = []any{"Basic"}
				return object(t, object(t, components, "responses"), "AuthenticationRequired")
			},
		},
		{
			name: "entity tag semantics", header: "ETag", want: "ETag schema",
			mutate: func(document map[string]any) map[string]any {
				response := object(t, object(t, object(t, object(t, document, "paths"), "/v1/work-items"), "post"), "responses")
				created := object(t, response, "201")
				object(t, object(t, object(t, created, "headers"), "ETag"), "schema")["pattern"] = "broken"
				return created
			},
		},
		{
			name: "idempotency semantics", header: "Idempotent-Replayed", want: "Idempotent-Replayed schema",
			mutate: func(document map[string]any) map[string]any {
				components := object(t, document, "components")
				object(t, object(t, object(t, components, "headers"), "IdempotentReplayed"), "schema")["enum"] = []any{"false"}
				response := object(t, object(t, object(t, object(t, document, "paths"), "/v1/work-items"), "post"), "responses")
				return object(t, response, "200")
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mutated := cloneOpenAPIDocument(t, document)
			response := test.mutate(mutated)
			err := validateRequiredHeader(mutated, response, test.header)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v want substring %q", err, test.want)
			}
		})
	}
}

func cloneOpenAPIDocument(t *testing.T, document map[string]any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	var clone map[string]any
	if err = json.Unmarshal(encoded, &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}

func resolveResponse(t *testing.T, document map[string]any, raw any) map[string]any {
	t.Helper()
	seen := map[string]bool{}
	for {
		response, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("response is %T", raw)
		}
		ref, _ := response["$ref"].(string)
		if ref == "" {
			return response
		}
		if seen[ref] {
			t.Fatalf("cyclic response ref %q", ref)
		}
		seen[ref] = true
		raw = resolveLocalRef(t, document, ref)
	}
}

func validateRequiredHeader(document, response map[string]any, name string) error {
	headers, ok := response["headers"].(map[string]any)
	if !ok {
		return fmt.Errorf("response omits %s", name)
	}
	raw, ok := headers[name]
	if !ok {
		return fmt.Errorf("response omits %s", name)
	}
	header, err := resolveReferencedObject(document, raw, "header")
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	rawSchema, ok := header["schema"]
	if !ok {
		return fmt.Errorf("%s header omits schema", name)
	}
	schema, err := resolveReferencedObject(document, rawSchema, "header schema")
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}

	switch name {
	case "X-Request-ID":
		if schema["type"] != "string" || schema["minLength"] != float64(1) || schema["maxLength"] != float64(128) {
			return fmt.Errorf("X-Request-ID schema=%v", schema)
		}
	case "WWW-Authenticate":
		if schema["type"] != "string" || !equalStrings(stringSlice(schema["enum"]), []string{`Bearer realm="work-management-api"`}) {
			return fmt.Errorf("WWW-Authenticate schema=%v", schema)
		}
	case "ETag":
		if schema["type"] != "string" || schema["pattern"] != `^"[1-9][0-9]*"$` {
			return fmt.Errorf("ETag schema=%v", schema)
		}
	case "Idempotent-Replayed":
		if schema["type"] != "string" || !equalStrings(stringSlice(schema["enum"]), []string{"true"}) {
			return fmt.Errorf("Idempotent-Replayed schema=%v", schema)
		}
	default:
		return fmt.Errorf("unsupported required header %q", name)
	}
	return nil
}

func resolveReferencedObject(document map[string]any, raw any, kind string) (map[string]any, error) {
	seen := map[string]bool{}
	for {
		object, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s is %T", kind, raw)
		}
		rawRef, referenced := object["$ref"]
		if !referenced {
			return object, nil
		}
		ref, ok := rawRef.(string)
		if !ok || ref == "" || !strings.HasPrefix(ref, "#/") {
			if text, isString := rawRef.(string); isString && strings.Contains(text, "://") {
				return nil, fmt.Errorf("unsupported non-local %s ref %q", kind, text)
			}
			return nil, fmt.Errorf("malformed %s ref %v", kind, rawRef)
		}
		if seen[ref] {
			return nil, fmt.Errorf("cyclic %s ref %q", kind, ref)
		}
		seen[ref] = true
		var err error
		raw, err = lookupLocalRef(document, ref)
		if err != nil {
			return nil, fmt.Errorf("%s ref %q: %w", kind, ref, err)
		}
	}
}

func lookupLocalRef(document map[string]any, ref string) (any, error) {
	var current any = document
	for _, escaped := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		if escaped == "" {
			return nil, fmt.Errorf("empty path segment")
		}
		key := strings.ReplaceAll(strings.ReplaceAll(escaped, "~1", "/"), "~0", "~")
		object, ok := current.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("traverses non-object at %q", key)
		}
		current, ok = object[key]
		if !ok {
			return nil, fmt.Errorf("missing segment %q", key)
		}
	}
	return current, nil
}

func resolveLocalRef(t *testing.T, document map[string]any, ref string) any {
	t.Helper()
	const prefix = "#/"
	if !strings.HasPrefix(ref, prefix) {
		t.Fatalf("unsupported non-local ref %q", ref)
	}
	var current any = document
	for _, escaped := range strings.Split(strings.TrimPrefix(ref, prefix), "/") {
		key := strings.ReplaceAll(strings.ReplaceAll(escaped, "~1", "/"), "~0", "~")
		object, ok := current.(map[string]any)
		if !ok {
			t.Fatalf("ref %q traverses non-object at %q", ref, key)
		}
		current, ok = object[key]
		if !ok {
			t.Fatalf("ref %q has missing segment %q", ref, key)
		}
	}
	return current
}

func assertClosedSchema(t *testing.T, schema map[string]any, properties, required []string) {
	t.Helper()
	if additional, ok := schema["additionalProperties"].(bool); !ok || additional {
		t.Errorf("schema additionalProperties=%v", schema["additionalProperties"])
	}
	if got := mapKeys(object(t, schema, "properties")); !sameStringSet(got, properties) {
		t.Errorf("schema properties=%v want=%v", got, properties)
	}
	if got := stringSlice(schema["required"]); !sameStringSet(got, required) {
		t.Errorf("schema required=%v want=%v", got, required)
	}
}

func operationKeys(pathItem map[string]any) []string {
	var result []string
	for key := range pathItem {
		switch key {
		case "get", "post", "put", "patch", "delete", "options", "head", "trace":
			result = append(result, key)
		}
	}
	return result
}

func mapKeys[V any](values map[string]V) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	return result
}

func sameStringSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	counts := make(map[string]int, len(left))
	for _, value := range left {
		counts[value]++
	}
	for _, value := range right {
		counts[value]--
	}
	for _, count := range counts {
		if count != 0 {
			return false
		}
	}
	return true
}
