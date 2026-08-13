#!/usr/bin/env bash
set -euo pipefail
API_URL="${API_URL:-http://localhost:${API_PORT:-8081}}"
API_ADMIN_URL="${API_ADMIN_URL:-http://127.0.0.1:${API_ADMIN_PORT:-9090}}"
MAILPIT_URL="${MAILPIT_URL:-http://localhost:${MAILPIT_UI_PORT:-8025}}"
MANAGER_TOKEN="${MANAGER_TOKEN:-dev-manager-token}"
ALICE_TOKEN="${ALICE_TOKEN:-dev-worker-alice-token}"
BOB_TOKEN="${BOB_TOKEN:-dev-worker-bob-token}"
ALICE_ID="22222222-2222-4222-8222-222222222222"
command -v curl >/dev/null; command -v jq >/dev/null
for _ in $(seq 1 60); do curl --fail --silent "$API_ADMIN_URL/health/ready" >/dev/null && break; sleep 1; done
key="demo-$(date +%s)-$$"
created="$(curl --fail --silent -X POST "$API_URL/v1/work-items" -H "Authorization: Bearer $MANAGER_TOKEN" -H 'Content-Type: application/json' -H "Idempotency-Key: $key" -d '{"title":"Inspect loading dock","description":"Verify the exception and document the findings."}')"
item_id="$(jq -r '.data.id' <<<"$created")"; version="$(jq -r '.data.version' <<<"$created")"
assigned="$(curl --fail --silent -X PUT "$API_URL/v1/work-items/$item_id/assignment" -H "Authorization: Bearer $MANAGER_TOKEN" -H 'Content-Type: application/json' -H "If-Match: \"$version\"" -d "{\"assignee_id\":\"$ALICE_ID\"}")"; version="$(jq -r '.data.version' <<<"$assigned")"
manager_status="$(curl --silent --output /dev/null --write-out '%{http_code}' -X POST "$API_URL/v1/work-items/$item_id/start" -H "Authorization: Bearer $MANAGER_TOKEN" -H "If-Match: \"$version\"")"; [[ "$manager_status" == "403" ]]
bob_status="$(curl --silent --output /dev/null --write-out '%{http_code}' "$API_URL/v1/work-items/$item_id" -H "Authorization: Bearer $BOB_TOKEN")"; [[ "$bob_status" == "404" ]]
queue="$(curl --fail --silent "$API_URL/v1/me/work-items?status=ASSIGNED" -H "Authorization: Bearer $ALICE_TOKEN")"; jq -e --arg id "$item_id" '.data | any(.id == $id)' <<<"$queue" >/dev/null
started="$(curl --fail --silent -X POST "$API_URL/v1/work-items/$item_id/start" -H "Authorization: Bearer $ALICE_TOKEN" -H "If-Match: \"$version\"")"; version="$(jq -r '.data.version' <<<"$started")"
completed="$(curl --fail --silent -X POST "$API_URL/v1/work-items/$item_id/complete" -H "Authorization: Bearer $ALICE_TOKEN" -H "If-Match: \"$version\"")"; jq -e '.data.status == "COMPLETED"' <<<"$completed" >/dev/null
history="$(curl --fail --silent "$API_URL/v1/work-items/$item_id/history" -H "Authorization: Bearer $ALICE_TOKEN")"; jq -e '.data | length == 4' <<<"$history" >/dev/null
for _ in $(seq 1 30); do messages="$(curl --fail --silent "$MAILPIT_URL/api/v1/messages")"; jq -e '.messages | any(any(.To[]?; .Address == "alice.worker@example.test"))' <<<"$messages" >/dev/null && break; sleep 1; done
for _ in $(seq 1 30); do delivery="$(curl --fail --silent "$API_URL/v1/notifications?work_item_id=$item_id" -H "Authorization: Bearer $MANAGER_TOKEN")"; jq -e '.data | length == 1 and .[0].status == "SENT"' <<<"$delivery" >/dev/null && break; sleep 1; done
echo "Demo completed successfully for work item $item_id"
