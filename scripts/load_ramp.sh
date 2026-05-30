set -e

ALGO=${1:-prequal}
LB_URL="http://localhost:8080"
SERVER1="http://localhost:9001"
SERVER2="http://localhost:9002"
SERVER3="http://localhost:9003"
TIMESTAMP=$(date +%Y%m%d_%H%M%S)
RESULTS_DIR="results"
OUTPUT_FILE="${RESULTS_DIR}/load_ramp_${ALGO}_${TIMESTAMP}.csv"

mkdir -p "${RESULTS_DIR}"

echo "================================================================"
echo " Prequal Load Ramp Experiment"
echo " Algorithm : ${ALGO}"
echo " Output    : ${OUTPUT_FILE}"
echo "================================================================"
echo ""

echo "step,load_pct,rps,algo,p50_ms,p99_ms,p999_ms,errors,requests" \
  > "${OUTPUT_FILE}"

wait_for_lb() {
  echo "waiting for load balancer..."
  for i in {1..30}; do
    if curl -sf "${LB_URL}/metrics" > /dev/null 2>&1; then
      echo "load balancer ready"
      return 0
    fi
    sleep 1
  done
  echo "ERROR: load balancer not ready after 30s"
  exit 1
}

reset_servers() {
  echo "resetting all servers to normal mode..."
  curl -sf "${SERVER1}/control?mode=normal" > /dev/null
  curl -sf "${SERVER2}/control?mode=normal" > /dev/null
  curl -sf "${SERVER3}/control?mode=normal" > /dev/null
  sleep 1
}


run_step() {
  local step=$1
  local load_pct=$2
  local rps=$3
  local duration=$4

  echo ""
  echo "── Step ${step}: ${load_pct}% load — ${rps} rps ──────────────"


  if [ "${step}" -ge 4 ]; then
    echo "  simulating above-allocation: server3 slow mode"
    curl -sf "${SERVER3}/control?mode=slow" > /dev/null
  else
    curl -sf "${SERVER3}/control?mode=normal" > /dev/null
  fi

 local result
  SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"

  result=$(cd "$PROJECT_ROOT" && go run cmd/gen/main.go \
    --target="${LB_URL}/api" \
    --rps="${rps}" \
    --dur="${duration}s" \
    --c=50 \
    --variable=true \
    2>&1 | tail -2 | head -1)

  echo "  raw output: ${result}"

  local requests errors p50 p99 p999

  requests=$(echo "${result}" | grep -oE 'requests=[0-9]+' | cut -d= -f2)
  errors=$(echo "${result}"   | grep -oE 'errors=[0-9]+'   | cut -d= -f2)
  p50=$(echo "${result}"      | grep -oE 'p50=[0-9]+'      | cut -d= -f2)
  p99=$(echo "${result}"      | grep -oE 'p99=[0-9]+'      | cut -d= -f2)
  p999=$(echo "${result}"     | grep -oE 'p999=[0-9]+'     | cut -d= -f2)

  requests=${requests:-0}
  errors=${errors:-0}
  p50=${p50:-0}
  p99=${p99:-0}
  p999=${p999:-0}

  echo "  requests=${requests} errors=${errors} p50=${p50}ms p99=${p99}ms p999=${p999}ms"

  echo "${step},${load_pct},${rps},${ALGO},${p50},${p99},${p999},${errors},${requests}" \
    >> "${OUTPUT_FILE}"
}


wait_for_lb
reset_servers

echo ""
echo "starting load ramp — 9 steps — 20 seconds each"
echo "step 4+ simulates above-allocation contention on server3"
echo ""

STEP_DUR=20

run_step 1 75  100 ${STEP_DUR}
run_step 2 83  111 ${STEP_DUR}
run_step 3 93  124 ${STEP_DUR}
run_step 4 103 137 ${STEP_DUR}
run_step 5 114 152 ${STEP_DUR}
run_step 6 127 169 ${STEP_DUR}
run_step 7 141 188 ${STEP_DUR}
run_step 8 157 209 ${STEP_DUR}
run_step 9 174 232 ${STEP_DUR}

reset_servers

echo ""
echo " Experiment complete"
echo " Results saved to: ${OUTPUT_FILE}"
echo ""
cat "${OUTPUT_FILE}"