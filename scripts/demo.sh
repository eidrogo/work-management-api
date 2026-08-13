#!/usr/bin/env bash
set -euo pipefail

API_URL="${API_URL:-http://127.0.0.1:${API_PORT:-8081}}"
API_ADMIN_URL="${API_ADMIN_URL:-http://127.0.0.1:${API_ADMIN_PORT:-9090}}"
MAILPIT_URL="${MAILPIT_URL:-http://127.0.0.1:${MAILPIT_UI_PORT:-8025}}"
DEMO_POLL_ATTEMPTS="${DEMO_POLL_ATTEMPTS:-60}"
DEMO_POLL_INTERVAL="${DEMO_POLL_INTERVAL:-1}"
MANAGER_TOKEN="${MANAGER_TOKEN:-dev-manager-token}"
ALICE_TOKEN="${ALICE_TOKEN:-dev-worker-alice-token}"
BOB_TOKEN="${BOB_TOKEN:-dev-worker-bob-token}"
MANAGER_ID="11111111-1111-4111-8111-111111111111"
ALICE_ID="22222222-2222-4222-8222-222222222222"
ALICE_EMAIL="alice.worker@example.test"
MANAGER_NAME="Maya Manager"
ALICE_NAME="Alice Worker"

command -v curl >/dev/null || { echo "curl is required" >&2; exit 1; }
command -v jq >/dev/null || { echo "jq is required" >&2; exit 1; }
[[ "$DEMO_POLL_ATTEMPTS" =~ ^[1-9][0-9]*$ ]] || { echo "DEMO_POLL_ATTEMPTS must be positive" >&2; exit 1; }
[[ "$DEMO_POLL_INTERVAL" =~ ^([0-9]+([.][0-9]+)?|[.][0-9]+)$ ]] || { echo "DEMO_POLL_INTERVAL must be nonnegative" >&2; exit 1; }

tmpdir="$(mktemp -d)"
cleanup() { rm -r "$tmpdir"; }
trap cleanup EXIT
curl_common=(--silent --show-error --connect-timeout 2 --max-time 10)

poll_until() {
	local description=$1 attempts=$2 interval=$3
	shift 3
	local attempt
	for ((attempt = 1; attempt <= attempts; attempt++)); do
		if "$@"; then
			return 0
		fi
		((attempt == attempts)) || sleep "$interval"
	done
	printf 'Timed out waiting for %s after %d attempts.\n' "$description" "$attempts" >&2
	return 1
}

api_request() {
	local name=$1 method=$2 url=$3 token=$4 body=$5
	shift 5
	RESPONSE_BODY="$tmpdir/$name.body"
	RESPONSE_HEADERS="$tmpdir/$name.headers"
	local args=("${curl_common[@]}" --request "$method" --output "$RESPONSE_BODY" --dump-header "$RESPONSE_HEADERS" --write-out '%{http_code}')
	[[ -z "$token" ]] || args+=(--header "Authorization: Bearer $token")
	[[ "$body" == "__NO_BODY__" ]] || args+=(--data "$body")
	local header
	for header in "$@"; do
		args+=(--header "$header")
	done
	RESPONSE_STATUS="$(curl "${args[@]}" "$url")"
}

header_value() {
	local name=$1
	awk -v want="$(printf '%s' "$name" | tr '[:upper:]' '[:lower:]')" '
		BEGIN { FS=":" }
		{ key=tolower($1); if (key==want) { sub(/^[^:]*:[[:space:]]*/, ""); sub(/\r$/, ""); value=$0 } }
		END { print value }
	' "$RESPONSE_HEADERS"
}

assert_status() {
	local expected=$1 operation=$2
	if [[ "$RESPONSE_STATUS" != "$expected" ]]; then
		printf '%s returned HTTP %s (wanted %s): %s\n' "$operation" "$RESPONSE_STATUS" "$expected" "$(cat "$RESPONSE_BODY")" >&2
		return 1
	fi
}

fetch_json() {
	local destination=$1 url=$2
	curl "${curl_common[@]}" --fail --output "$destination" "$url"
}

api_ready() {
	local file="$tmpdir/readiness.json"
	fetch_json "$file" "$API_ADMIN_URL/health/ready" >/dev/null 2>&1 && jq -e '.status == "ready"' "$file" >/dev/null
}

mail_summary_ready() {
	fetch_json "$tmpdir/messages.json" "$MAILPIT_URL/api/v1/messages" >/dev/null 2>&1 &&
		jq -e --arg recipient "$ALICE_EMAIL" --arg subject "$subject" '
			.messages | map(select(.Subject == $subject and any(.To[]?; .Address == $recipient))) | length == 1
		' "$tmpdir/messages.json" >/dev/null
}

delivery_sent() {
	api_request notification-list GET "$API_URL/v1/notifications?work_item_id=$item_id" "$MANAGER_TOKEN" __NO_BODY__ >/dev/null 2>&1 || return 1
	[[ "$RESPONSE_STATUS" == "200" ]] && jq -e '.data | length == 1 and .[0].status == "SENT"' "$RESPONSE_BODY" >/dev/null
}

poll_until "API readiness" "$DEMO_POLL_ATTEMPTS" "$DEMO_POLL_INTERVAL" api_ready
echo "PASS: API admin readiness"

fetch_json "$tmpdir/mailpit-before.json" "$MAILPIT_URL/api/v1/info"
jq -e '(.Messages | type == "number") and (.RuntimeStats.SMTPAccepted | type == "number")' "$tmpdir/mailpit-before.json" >/dev/null
messages_before="$(jq -r '.Messages' "$tmpdir/mailpit-before.json")"
smtp_accepted_before="$(jq -r '.RuntimeStats.SMTPAccepted' "$tmpdir/mailpit-before.json")"

run_suffix="$(date -u +%Y%m%dT%H%M%SZ)-$$-${RANDOM}"
title="Inspect loading dock $run_suffix"
description="Verify the exception and document the findings for $run_suffix."
key="demo-$run_suffix"

api_request create POST "$API_URL/v1/work-items" "$MANAGER_TOKEN" "$(jq -cn --arg title "$title" --arg description "$description" '{title:$title,description:$description}')" \
	"Content-Type: application/json" "Idempotency-Key: $key"
assert_status 201 "manager create"
jq -e --arg title "$title" --arg description "$description" --arg creator "$MANAGER_ID" --arg creator_name "$MANAGER_NAME" '
	.data.id | test("^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$")
' "$RESPONSE_BODY" >/dev/null
jq -e --arg title "$title" --arg description "$description" --arg creator "$MANAGER_ID" --arg creator_name "$MANAGER_NAME" '
	.data.title == $title and .data.description == $description and .data.status == "UNASSIGNED" and
	.data.creator_id == $creator and .data.creator_name == $creator_name and
	(.data | has("assignee_id") | not) and .data.version == 1
' "$RESPONSE_BODY" >/dev/null
item_id="$(jq -r '.data.id' "$RESPONSE_BODY")"
version="$(jq -r '.data.version' "$RESPONSE_BODY")"
[[ "$(header_value ETag)" == "\"$version\"" ]]
echo "PASS: manager created current work item"

api_request assign PUT "$API_URL/v1/work-items/$item_id/assignment" "$MANAGER_TOKEN" "$(jq -cn --arg assignee "$ALICE_ID" '{assignee_id:$assignee}')" \
	"Content-Type: application/json" "If-Match: \"$version\""
assert_status 200 "manager assignment"
jq -e --arg id "$item_id" --arg assignee "$ALICE_ID" --arg name "$ALICE_NAME" --argjson prior "$version" '
	.data.id == $id and .data.status == "ASSIGNED" and .data.assignee_id == $assignee and
	.data.assignee_name == $name and .data.version == ($prior + 1) and .meta.idempotent == false
' "$RESPONSE_BODY" >/dev/null
version="$(jq -r '.data.version' "$RESPONSE_BODY")"
[[ "$(header_value ETag)" == "\"$version\"" ]]
echo "PASS: manager assigned Alice"

api_request manager-start POST "$API_URL/v1/work-items/$item_id/start" "$MANAGER_TOKEN" __NO_BODY__ "If-Match: \"$version\""
assert_status 403 "manager start denial"
request_id="$(header_value X-Request-ID)"
jq -e --arg request_id "$request_id" '.error.code == "PERMISSION_DENIED" and .error.request_id == $request_id and ($request_id | length > 0)' "$RESPONSE_BODY" >/dev/null

api_request bob-get GET "$API_URL/v1/work-items/$item_id" "$BOB_TOKEN" __NO_BODY__
assert_status 404 "other-worker item non-disclosure"
jq -e '.error.code == "RESOURCE_NOT_FOUND"' "$RESPONSE_BODY" >/dev/null
api_request bob-queue GET "$API_URL/v1/me/work-items" "$BOB_TOKEN" __NO_BODY__
assert_status 200 "other-worker queue"
jq -e --arg id "$item_id" '.data | all(.id != $id)' "$RESPONSE_BODY" >/dev/null
echo "PASS: manager progress denied and Bob cannot discover the item"

api_request alice-queue GET "$API_URL/v1/me/work-items?status=ASSIGNED" "$ALICE_TOKEN" __NO_BODY__
assert_status 200 "Alice assigned queue"
jq -e --arg id "$item_id" '.data | length == 1 and .[0].id == $id and .[0].status == "ASSIGNED"' "$RESPONSE_BODY" >/dev/null

api_request start POST "$API_URL/v1/work-items/$item_id/start" "$ALICE_TOKEN" __NO_BODY__ "If-Match: \"$version\""
assert_status 200 "Alice start"
jq -e --arg id "$item_id" --argjson prior "$version" '.data.id == $id and .data.status == "IN_PROGRESS" and .data.version == ($prior + 1)' "$RESPONSE_BODY" >/dev/null
version="$(jq -r '.data.version' "$RESPONSE_BODY")"
[[ "$(header_value ETag)" == "\"$version\"" ]]

api_request complete POST "$API_URL/v1/work-items/$item_id/complete" "$ALICE_TOKEN" __NO_BODY__ "If-Match: \"$version\""
assert_status 200 "Alice complete"
jq -e --arg id "$item_id" --argjson prior "$version" '.data.id == $id and .data.status == "COMPLETED" and .data.version == ($prior + 1)' "$RESPONSE_BODY" >/dev/null
version="$(jq -r '.data.version' "$RESPONSE_BODY")"
[[ "$(header_value ETag)" == "\"$version\"" ]]

api_request history GET "$API_URL/v1/work-items/$item_id/history" "$ALICE_TOKEN" __NO_BODY__
assert_status 200 "work-item history"
jq -e --arg manager "$MANAGER_ID" --arg alice "$ALICE_ID" '
	(.data | length == 4) and
	([.data[].type] == ["WORK_ITEM_CREATED","WORK_ITEM_ASSIGNED","WORK_ITEM_MARKED_IN_PROGRESS","WORK_ITEM_COMPLETED"]) and
	([.data[].actor_id] == [$manager,$manager,$alice,$alice]) and .data[1].new_assignee_id == $alice
' "$RESPONSE_BODY" >/dev/null
echo "PASS: Alice started and completed the item with ordered history"

subject="New work assigned: $title"
poll_until "current Mailpit message" "$DEMO_POLL_ATTEMPTS" "$DEMO_POLL_INTERVAL" mail_summary_ready
mailpit_id="$(jq -r --arg recipient "$ALICE_EMAIL" --arg subject "$subject" '.messages[] | select(.Subject == $subject and any(.To[]?; .Address == $recipient)) | .ID' "$tmpdir/messages.json")"
fetch_json "$tmpdir/message.json" "$MAILPIT_URL/api/v1/message/$mailpit_id"
jq -e --arg recipient "$ALICE_EMAIL" --arg subject "$subject" --arg id "$item_id" --arg title "$title" --arg manager "$MANAGER_NAME" --arg alice "$ALICE_NAME" '
	.Subject == $subject and any(.To[]?; .Address == $recipient) and
	(.Text | contains($id) and contains($title) and contains($manager) and contains($alice))
' "$tmpdir/message.json" >/dev/null
fetch_json "$tmpdir/mailpit-after.json" "$MAILPIT_URL/api/v1/info"
jq -e --argjson messages "$messages_before" --argjson accepted "$smtp_accepted_before" \
	'.Messages == ($messages + 1) and .RuntimeStats.SMTPAccepted == ($accepted + 1)' "$tmpdir/mailpit-after.json" >/dev/null
echo "PASS: Mailpit accepted exactly one current SMTP transaction"

poll_until "durable SENT notification" "$DEMO_POLL_ATTEMPTS" "$DEMO_POLL_INTERVAL" delivery_sent
notification_id="$(jq -r '.data[0].id' "$RESPONSE_BODY")"
api_request notification-detail GET "$API_URL/v1/notifications/$notification_id" "$MANAGER_TOKEN" __NO_BODY__
assert_status 200 "notification detail"
jq -e --arg recipient "$ALICE_EMAIL" --arg item "$item_id" '
	.data.recipient_email == $recipient and .data.work_item_id == $item and .data.status == "SENT" and
	.data.delivery_cycle == 1 and .data.sent_at != null and
	((.data.last_error_code // "") == "") and ((.data.last_error // "") == "") and
	(.data.attempts | length == 1 and .[0].attempt_number == 1 and .[0].outcome == "SENT") and
	(.data | has("lease_token") | not)
' "$RESPONSE_BODY" >/dev/null
notification_message_id="$(jq -r '.data.message_id | ltrimstr("<") | rtrimstr(">")' "$RESPONSE_BODY")"
jq -e --arg message_id "$notification_message_id" '.MessageID == $message_id' "$tmpdir/message.json" >/dev/null
jq -e --arg message_id "$notification_message_id" --arg recipient "$ALICE_EMAIL" --arg subject "$subject" '
	.messages | map(select(.Subject == $subject and .MessageID == $message_id and any(.To[]?; .Address == $recipient))) | length == 1
' "$tmpdir/messages.json" >/dev/null
echo "PASS: durable SENT detail matches the current Mailpit Message-ID and attempt"
echo "Demo completed successfully for work item $item_id"
