
set -e

LB_URL="http://localhost:8080"
SERVER1="http://localhost:9001"
SERVER2="http://localhost:9002"
SERVER3="http://localhost:9003"
TIMESTAMP=$(date +%Y%m%d_%H%M%S)
RESULTS_DIR="results"
OUTPUT_FILE="${RESULTS_DIR}/replica_selection_${TIMESTAMP}.csv"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"

mkdir -p "${RESULTS_DIR}"

echo "================================================================"
echo " Prequal Replica Selection Comparison (§5.2)"
echo " Output: ${OUTPUT_FILE}"
echo "================================================================"
echo ""

echo "algo,load_pct,rps,p90_ms,p99_ms,errors,requests" > "${OUTPUT_FILE}"



wait_for_lb() {
  echo "waiting for load balancer at ${LB_URL}..."
  for i in {1..30}; do
    if curl -sf "${LB_URL}/metrics" > /dev/null 2>&1; then
      echo "load balancer ready"
      return 0
    fi
    sleep 1
  done
  echo "ERROR: load balancer not ready"
  exit 1
}

reset_servers() {
  curl -sf "${SERVER1}/control?mode=normal" > /dev/null
  curl -sf "${SERVER2}/control?mode=normal" > /dev/null
  curl -sf "${SERVER3}/control?mode=normal" > /dev/null
  sleep 2
}

make_server3_slow_hardware() {
  echo "  server3: simulating older hardware (2x slower)"
  curl -sf "${SERVER3}/control?mode=slow" > /dev/null
}

restart_lb_with_algo() {
  local algo=$1
  echo ""
  echo "── switching load balancer to: ${algo} ──────────────────────"

  cd "$PROJECT_ROOT"
  sed -i.bak "s/--algo=[a-z]*/--algo=${algo}/" docker-compose.yml

  echo "  docker-compose algo now: $(grep 'algo' docker-compose.yml | grep -v '#' | head -1 | tr -d ' ')"

  docker-compose up -d --no-deps --build lb > /dev/null 2>&1

  echo "  waiting for lb to restart..."
  sleep 8
  wait_for_lb
}

run_load_test() {
  local algo=$1
  local load_pct=$2
  local rps=$3
  local duration=30

  echo ""
  echo "  testing: algo=${algo} load=${load_pct}% rps=${rps}"

  local result
  result=$(cd "$PROJECT_ROOT" && go run cmd/gen/main.go \
    --target="${LB_URL}/api" \
    --rps="${rps}" \
    --dur="${duration}s" \
    --c=50 \
    --variable=true \
    2>&1 | grep -A1 "final results" | tail -1)

  echo "  raw: ${result}"

  local requests errors p90 p99
  requests=$(echo "${result}" | grep -oE 'requests=[0-9]+' | cut -d= -f2)
  errors=$(echo "${result}"   | grep -oE 'errors=[0-9]+'   | cut -d= -f2)
  p99=$(echo "${result}"      | grep -oE 'p99=[0-9]+'      | cut -d= -f2)
  p90=$(echo "${result}"      | grep -oE 'p50=[0-9]+'      | cut -d= -f2)

  requests=${requests:-0}
  errors=${errors:-0}
  p90=${p90:-0}
  p99=${p99:-0}

  echo "  result: requests=${requests} errors=${errors} p90=${p90}ms p99=${p99}ms"

  echo "${algo},${load_pct},${rps},${p90},${p99},${errors},${requests}" \
    >> "${OUTPUT_FILE}"
}


wait_for_lb

echo ""
echo "experiment design:"
echo "  algorithms : round_robin (rr), wrr, prequal"
echo "  load levels: 70% (100 rps) and 90% (130 rps)"
echo "  server3    : simulated older hardware (2x slower)"
echo "  duration   : 30s per combination = 3 algos x 2 loads = 6 runs"
echo ""

make_server3_slow_hardware

restart_lb_with_algo "rr"
echo ""
echo "=== Round Robin ==="
run_load_test "round_robin" 70 100
run_load_test "round_robin" 90 130

restart_lb_with_algo "wrr"
echo ""
echo "=== Weighted Round Robin ==="
run_load_test "wrr" 70 100
run_load_test "wrr" 90 130

restart_lb_with_algo "prequal"
echo ""
echo "=== Prequal (HCL) ==="
run_load_test "prequal" 70 100
run_load_test "prequal" 90 130

reset_servers

cd "$PROJECT_ROOT"
sed -i.bak "s/--algo=[a-z]*/--algo=prequal/" docker-compose.yml

echo ""
echo "================================================================"
echo " Experiment complete"
echo " Results: ${OUTPUT_FILE}"
echo "================================================================"
echo ""
cat "${OUTPUT_FILE}"
echo ""
echo "generate chart:"
echo "  python3 scripts/plot_replica_selection.py ${OUTPUT_FILE}"