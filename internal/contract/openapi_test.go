package contract

import (
	"encoding/json"
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
	if !hasHeader(assignmentOK, "Idempotent-Replayed") {
		t.Fatal("assignment 200 omits Idempotent-Replayed")
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
					if !hasHeader(response, "X-Request-ID") {
						t.Errorf("%s %s %s omits X-Request-ID", method, path, status)
					}
				}
			}
		}
	}
	for name, rawResponse := range object(t, components, "responses") {
		if !hasHeader(rawResponse.(map[string]any), "X-Request-ID") {
			t.Errorf("component response %s omits X-Request-ID", name)
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

func hasHeader(response map[string]any, name string) bool {
	headers, ok := response["headers"].(map[string]any)
	if !ok {
		return false
	}
	_, ok = headers[name]
	return ok
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
				if !hasHeader(response, "X-Request-ID") {
					t.Errorf("%s %s %s omits X-Request-ID", strings.ToUpper(method), path, status)
				}
				if status == "401" && !hasHeader(response, "WWW-Authenticate") {
					t.Errorf("%s %s 401 omits WWW-Authenticate", strings.ToUpper(method), path)
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
		response := object(t, object(t, object(t, object(t, paths, required.path), required.method), "responses"), required.status)
		if !hasHeader(response, required.header) {
			t.Errorf("%s %s %s omits %s", strings.ToUpper(required.method), required.path, required.status, required.header)
		}
	}
	header := object(t, object(t, components, "headers"), "IdempotentReplayed")
	schema := object(t, header, "schema")
	if schema["type"] != "string" || !sameStringSet(stringSlice(schema["enum"]), []string{"true"}) {
		t.Fatalf("IdempotentReplayed schema=%v", schema)
	}
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
