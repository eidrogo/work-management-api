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
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve contract path")
	}
	content, err := os.ReadFile(filepath.Join(filepath.Dir(file), "../../api/openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		OpenAPI string                                `json:"openapi"`
		Paths   map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err = json.Unmarshal(content, &document); err != nil {
		t.Fatal(err)
	}
	if document.OpenAPI != "3.1.0" {
		t.Fatalf("openapi=%q", document.OpenAPI)
	}
	for path, methods := range map[string][]string{"/v1/work-items": {"get", "post"}, "/v1/me/work-items": {"get"}, "/v1/notifications": {"get"}} {
		item, ok := document.Paths[path]
		if !ok {
			t.Errorf("missing path %s", path)
			continue
		}
		for _, method := range methods {
			if _, ok := item[method]; !ok {
				t.Errorf("missing %s %s", method, path)
			}
		}
	}
}
