#!/bin/sh
# End-to-end check of the console against KTMS and light-tokenmeter built
# from their main branches (or KTMS_REF / TOKENMETER_REF), with
# light-tokenmeter's authentication on and a mock vendor.
set -eu

cd "$(dirname "$0")/.."
# The tested pair comes from e2e.refs, so a pull request is not failed by an
# unrelated upstream change. The daily run uses main.
. ./scripts/e2e.refs
KTMS_REF=${KTMS_REF:-$E2E_KTMS_REF}
TOKENMETER_REF=${TOKENMETER_REF:-$E2E_TOKENMETER_REF}
P=ktms-admin-e2e-$$
NET=$P-net
WORK=$(mktemp -d)
ADMIN_PORT=${ADMIN_PORT:-18090}
CONSOLE="http://127.0.0.1:$ADMIN_PORT"

cleanup() {
	docker rm -f -v "$P-pg" "$P-redis" "$P-mock" "$P-tm" "$P-ktms" "$P-admin" >/dev/null 2>&1 || true
	docker network rm "$NET" >/dev/null 2>&1 || true
	docker rmi -f "$P-admin-img" "$P-ktms-img" "$P-tm-img" "$P-mock-img" >/dev/null 2>&1 || true
	rm -rf "$WORK"
}
trap cleanup EXIT
trap 'exit 130' INT TERM

fail() {
	echo "e2e: $*" >&2
	for c in ktms tm admin; do
		echo "--- $c logs" >&2
		docker logs "$P-$c" 2>&1 | tail -n 20 >&2 || true
	done
	exit 1
}

# api METHOD PATH [JSON]: calls the console the way its UI does. Requests take
# protojson field names; responses use the proto names (plain_key). A refused
# call prints the console's error body to stderr.
api() {
	out=$(curl -sS --connect-timeout 5 --max-time 60 -w '\n%{http_code}' -X "$1" "$CONSOLE$2" \
		-H 'X-Requested-With: ktms-admin' -H 'Content-Type: application/json' ${3:+-d "$3"}) || return 1
	code=$(printf '%s\n' "$out" | tail -n 1)
	body=$(printf '%s\n' "$out" | sed '$d')
	case $code in
	2??) printf '%s\n' "$body" ;;
	*) echo "$1 $2 -> HTTP $code: $body" >&2; return 1 ;;
	esac
}

# build NAME CONTEXT [ARGS]: keeps the log quiet unless the build fails.
build() {
	name=$1; shift
	docker build -t "$P-$name-img" "$@" >"$WORK/$name.build.log" 2>&1 || { tail -n 40 "$WORK/$name.build.log" >&2; fail "building $name"; }
}

wait_for() {
	i=0
	until curl -fsS --max-time 5 "$1" >/dev/null 2>&1; do
		i=$((i + 1))
		[ "$i" -gt 90 ] && fail "$1 did not answer"
		sleep 1
	done
}

echo "e2e: building ktms@$KTMS_REF, light-tokenmeter@$TOKENMETER_REF and this console"
git clone -q --depth 1 --branch "$KTMS_REF" https://github.com/ai-process/ktms.git "$WORK/ktms" || fail "cloning ktms@$KTMS_REF"
git clone -q --depth 1 --branch "$TOKENMETER_REF" https://github.com/ai-process/light-tokenmeter.git "$WORK/tm" || fail "cloning light-tokenmeter@$TOKENMETER_REF"
build admin .
build ktms --target slim "$WORK/ktms"
build mock "$WORK/ktms/bench/mock-upstream"
build tm "$WORK/tm"

W=$(openssl rand -hex 16)
R=$(openssl rand -hex 16)
h() { printf %s "$1" | openssl dgst -sha256 -r | cut -d' ' -f1; }
BOOTSTRAP=$(openssl rand -hex 16)
KEYS="1:$(openssl rand -base64 32)"

docker network create "$NET" >/dev/null
docker run -d --name "$P-pg" --network "$NET" -e POSTGRES_PASSWORD=pg postgres:17-alpine >/dev/null
docker run -d --name "$P-redis" --network "$NET" redis:7-alpine >/dev/null
docker run -d --name "$P-mock" --network "$NET" -e MOCK_REQUIRE_KEY=vk-e2e "$P-mock-img" >/dev/null
i=0
until docker exec "$P-pg" pg_isready -h 127.0.0.1 -U postgres >/dev/null 2>&1; do
	i=$((i + 1))
	[ "$i" -gt 60 ] && fail "Postgres did not become ready"
	sleep 1
done
docker exec "$P-pg" psql -qU postgres -c "CREATE DATABASE ktms" -c "CREATE DATABASE tokenmeter" >/dev/null || fail "creating databases"

docker run -d --name "$P-tm" --network "$NET" \
	-e DATABASE_URL="postgres://postgres:pg@$P-pg:5432/tokenmeter?sslmode=disable" \
	-e TOKENMETER_TOKENS="ktms:write:$(h "$W"),console:read:$(h "$R")" "$P-tm-img" >/dev/null
docker run -d --name "$P-ktms" --network "$NET" \
	-e DATABASE_URL="postgres://postgres:pg@$P-pg:5432/ktms?sslmode=disable" -e KTMS_ENCRYPTION_KEYS="$KEYS" \
	-e BOOTSTRAP_ADMIN_KEY="$BOOTSTRAP" -e REDIS_ADDR="$P-redis:6379" -e ROUTING_RELOAD_SECONDS=1 \
	-e USAGE_COLLECTOR_HOST="$P-tm:9090" -e USAGE_COLLECTOR_TOKEN="$W" "$P-ktms-img" >/dev/null

# run_console KEY TOKEN starts the console on localhost only, without sign-in.
run_console() {
	docker rm -f "$P-admin" >/dev/null 2>&1 || true
	docker run -d --name "$P-admin" --network "$NET" -p "127.0.0.1:$ADMIN_PORT:8080" \
		-e PUBLIC_URL="$CONSOLE" -e INSECURE_NO_AUTH=true \
		-e KTMS_ADDR="$P-ktms:9090" -e KTMS_ADMIN_KEY="$1" \
		-e TOKENMETER_URL="http://$P-tm:8080" -e TOKENMETER_TOKEN="$2" "$P-admin-img" >/dev/null
	wait_for "$CONSOLE/healthz"
}

# The bootstrap key mints the console's own key, as an operator would.
run_console "$BOOTSTRAP" "$R"
i=0
until api GET /api/health 2>/dev/null | jq -e '.proxy.ok and .usage.ok' >/dev/null; do
	i=$((i + 1))
	[ "$i" -gt 60 ] && fail "backends not healthy: $(api GET /api/health)"
	sleep 1
done
CONSOLE_KEY=$(api POST /api/proxy/keys '{"name":"console","scopes":["admin"]}' | jq -r .plain_key)
[ -n "$CONSOLE_KEY" ] && [ "$CONSOLE_KEY" != null ] || fail "minting the console key failed"
run_console "$CONSOLE_KEY" "$R"
api GET /api/health | jq -e '.proxy.ok and .usage.ok' >/dev/null || fail "health with the console key: $(api GET /api/health)"
echo "e2e: both backends reachable, light-tokenmeter with a read token"

api PUT /api/proxy/models/mock-gpt '{"id":"mock-gpt","vendor":"openai","endpoint":"http://'"$P-mock"':8000/v1","efforts":["EFFORT_LOW","EFFORT_MEDIUM","EFFORT_HIGH"],"priceInPerMtok":1,"priceOutPerMtok":2,"enabled":true}' >/dev/null ||
	fail "upsert model"
api PUT /api/proxy/vendor-keys '{"apiKeyName":"console","vendor":"openai","plainKey":"vk-e2e"}' >/dev/null || fail "set vendor key"
api PUT /api/proxy/rules '{"rules":[{"name":"catch-all","priority":10,"use":["mock-gpt"],"enabled":true}]}' >/dev/null || fail "replace rules"
api GET /api/proxy/config | jq -e '(.models | map(.id) | index("mock-gpt")) and (.rules | map(.name) | index("catch-all"))' >/dev/null ||
	fail "config does not show the model and rule: $(api GET /api/proxy/config)"
echo "e2e: model, vendor key and rule configured through the console"

api POST /api/proxy/keys '{"name":"e2e-client","scopes":["generate"]}' | jq -e '.plain_key | startswith("ktms_")' >/dev/null || fail "mint client key"
api GET /api/proxy/keys | jq -e '.keys | map(.name) | index("e2e-client")' >/dev/null || fail "client key not listed"
api PUT /api/proxy/spend-limits '{"subjectType":"key","subjectId":"e2e-client","period":"day","limitNanousd":"5000000000"}' >/dev/null ||
	fail "set spend limit"
api GET /api/proxy/spend-limits | jq -e '.spend_limits | map(.subject_id) | index("e2e-client")' >/dev/null || fail "spend limit not listed: $(api GET /api/proxy/spend-limits)"
api PUT /api/proxy/keys/e2e-client/policy '{"allowedModels":["mock-gpt"],"rpmLimit":60,"tpmLimit":"100000"}' >/dev/null || fail "set key policy"
api GET /api/proxy/keys | jq -e '.keys[] | select(.name == "e2e-client") | .policy.rpm_limit == 60 and .policy.allowed_models == ["mock-gpt"]' >/dev/null ||
	fail "key policy not listed: $(api GET /api/proxy/keys)"
i=0
until api GET "/api/proxy/spend?subject_type=key&subject_id=e2e-client" 2>/dev/null | jq -e '.windows[] | select(.period == "day") | .limit_nanousd == "5000000000"' >/dev/null; do
	i=$((i + 1))
	[ "$i" -gt 15 ] && fail "spend of the key: $(api GET "/api/proxy/spend?subject_type=key&subject_id=e2e-client")"
	sleep 1
done
ORG=$(api POST /api/proxy/organizations '{"name":"e2e-org"}' | jq -r .organization.id) || true
[ -n "$ORG" ] && [ "$ORG" != null ] || fail "create organization"
api POST /api/proxy/teams '{"orgId":"'"$ORG"'","name":"e2e-team","monthlyBudgetUsd":10,"allowedModels":["mock-gpt"]}' >/dev/null || fail "create team"
api GET /api/proxy/teams | jq -e '.teams | map(.name) | index("e2e-team")' >/dev/null || fail "team not listed"
api DELETE /api/proxy/keys/e2e-client >/dev/null || fail "revoke client key"
api GET /api/proxy/keys | jq -e '.keys | map(select(.name == "e2e-client" and (.disabled | not))) | length == 0' >/dev/null ||
	fail "the revoked key is still active: $(api GET /api/proxy/keys)"
echo "e2e: keys, key policies, spend limits and spend, organizations and teams work"

# Before the call: a run that crosses midnight UTC still finds the event.
SINCE=$(date -u +%F)
i=0
until OUT=$(api POST /api/proxy/playground '{"prompt":"hello","effort":"low"}' 2>/dev/null) &&
	echo "$OUT" | jq -e '.resolved_model == "mock-gpt" and (.choices | length > 0)' >/dev/null; do
	i=$((i + 1))
	[ "$i" -gt 30 ] && fail "playground did not answer through KTMS: ${OUT:-}"
	sleep 1
done
echo "e2e: playground answered through KTMS and the mock vendor"

DAY=$(date -u +%F)
i=0
until api GET "/api/usage?from=$SINCE&to=$DAY&group=model" | jq -e '(.report.groups // []) | map(select(.key == "mock-gpt" and .requests >= 1)) | length > 0' >/dev/null; do
	i=$((i + 1))
	[ "$i" -gt 30 ] && fail "usage did not reach the console: $(api GET "/api/usage?from=$SINCE&to=$DAY&group=model")"
	sleep 1
done
echo "e2e: the call's usage went KTMS -> light-tokenmeter (write token) -> console (read token)"

# A token without the read scope must show as refused, not as down.
run_console "$CONSOLE_KEY" "$W"
api GET /api/health | jq -e '.usage.ok == false and .usage.auth == true' >/dev/null || fail "a write-only token was not reported as refused: $(api GET /api/health)"
echo "e2e: a token without the read scope is reported as refused"
echo "e2e passed"
