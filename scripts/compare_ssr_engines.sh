#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

ROUNDS=3
CONCURRENCY=6
MAX_TIME=5
SAMPLE_INTERVAL=0.5
INCLUDE_SLOW=0
PORT=8080
RENDER_LIMIT=""
OUT_DIR=""

usage() {
  cat <<'USAGE'
用法:
  scripts/compare_ssr_engines.sh [options]

选项:
  --rounds N            每个路由的测试轮数（默认: 3）
  --concurrency N       每个路由的并发请求数（默认: 6）
  --max-time SEC        单请求超时秒数（默认: 5）
  --sample-interval SEC 资源采样间隔秒（默认: 0.5）
  --include-slow        追加 slow-ssr 路由
  --port PORT           服务端口（默认: 8080，当前 example 固定监听 8080）
  --render-limit N      设置 SSR_RENDER_LIMIT
  --out DIR             输出目录（默认: benchmark_output/<timestamp>）
  -h, --help            显示帮助
USAGE
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "缺少命令: $1" >&2
    exit 1
  }
}

is_positive_int() {
  [[ "$1" =~ ^[0-9]+$ ]] && [[ "$1" -ge 1 ]]
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --rounds)
      ROUNDS="$2"
      shift 2
      ;;
    --concurrency)
      CONCURRENCY="$2"
      shift 2
      ;;
    --max-time)
      MAX_TIME="$2"
      shift 2
      ;;
    --sample-interval)
      SAMPLE_INTERVAL="$2"
      shift 2
      ;;
    --include-slow)
      INCLUDE_SLOW=1
      shift
      ;;
    --port)
      PORT="$2"
      shift 2
      ;;
    --render-limit)
      RENDER_LIMIT="$2"
      shift 2
      ;;
    --out)
      OUT_DIR="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "未知参数: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

require_cmd go
require_cmd curl
require_cmd awk
require_cmd sort
require_cmd ps
require_cmd mktemp

if ! is_positive_int "${ROUNDS}"; then
  echo "--rounds 必须是正整数" >&2
  exit 1
fi
if ! is_positive_int "${CONCURRENCY}"; then
  echo "--concurrency 必须是正整数" >&2
  exit 1
fi
if ! is_positive_int "${PORT}"; then
  echo "--port 必须是正整数" >&2
  exit 1
fi
if [[ "${PORT}" -ne 8080 ]]; then
  echo "当前 example/main.go 固定监听 :8080，暂不支持 --port != 8080" >&2
  exit 1
fi
if [[ -n "${RENDER_LIMIT}" ]] && ! is_positive_int "${RENDER_LIMIT}"; then
  echo "--render-limit 必须是正整数" >&2
  exit 1
fi

DIST_INDEX="${ROOT_DIR}/example/web/dist/client/index.html"
DIST_SERVER="${ROOT_DIR}/example/web/dist/server/server.js"
if [[ ! -f "${DIST_INDEX}" || ! -f "${DIST_SERVER}" ]]; then
  echo "缺少前端构建产物，请先执行: cd example && make web-build" >&2
  exit 1
fi

port_in_use() {
  if command -v lsof >/dev/null 2>&1; then
    lsof -nP -iTCP:"${PORT}" -sTCP:LISTEN >/dev/null 2>&1
    return
  fi
  if command -v nc >/dev/null 2>&1; then
    nc -z 127.0.0.1 "${PORT}" >/dev/null 2>&1
    return
  fi
  curl -sS --max-time 0.2 "http://127.0.0.1:${PORT}" >/dev/null 2>&1
}

if port_in_use; then
  echo "端口 ${PORT} 已被占用，请先停止占用进程或使用 --port" >&2
  exit 1
fi

if [[ -z "${OUT_DIR}" ]]; then
  OUT_DIR="${ROOT_DIR}/benchmark_output/$(date +%Y%m%d_%H%M%S)"
fi
mkdir -p "${OUT_DIR}"

BIN_PATH="${OUT_DIR}/example_server"
SUMMARY_CSV="${OUT_DIR}/summary.csv"
REPORT_TXT="${OUT_DIR}/report.txt"
REQ_V8="${OUT_DIR}/requests_v8.csv"
REQ_GOJA="${OUT_DIR}/requests_goja.csv"
SAMPLE_V8="${OUT_DIR}/samples_v8.csv"
SAMPLE_GOJA="${OUT_DIR}/samples_goja.csv"
LOG_V8="${OUT_DIR}/server_v8.log"
LOG_GOJA="${OUT_DIR}/server_goja.log"

ROUTES=(
  "/"
  "/en"
  "/zh"
  "/hi/gopher"
  "/en/hi/gopher"
  "/zh/hi/gopher"
  "/hi/vue?title=Ms."
  "/seo-demo?title=SSR%20SEO%20Title"
  "/session-demo"
  "/no-ssr-fetch"
  "/en/no-ssr-fetch"
  "/zh/no-ssr-fetch"
  "/protected"
)
if [[ "${INCLUDE_SLOW}" -eq 1 ]]; then
  ROUTES+=("/slow-ssr" "/en/slow-ssr" "/zh/slow-ssr")
fi

TOTAL_EXPECTED=$((ROUNDS * CONCURRENCY * ${#ROUTES[@]}))
BASE_URL="http://127.0.0.1:${PORT}"

SERVER_PID=""
SAMPLER_PID=""

cleanup() {
  if [[ -n "${SAMPLER_PID}" ]] && kill -0 "${SAMPLER_PID}" 2>/dev/null; then
    kill "${SAMPLER_PID}" >/dev/null 2>&1 || true
    wait "${SAMPLER_PID}" >/dev/null 2>&1 || true
  fi
  if [[ -n "${SERVER_PID}" ]] && kill -0 "${SERVER_PID}" 2>/dev/null; then
    kill "${SERVER_PID}" >/dev/null 2>&1 || true
    wait "${SERVER_PID}" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT INT TERM

wait_server_ready() {
  local pid="$1"
  local log_file="$2"
  local i
  for i in $(seq 1 80); do
    if ! kill -0 "${pid}" 2>/dev/null; then
      echo "服务启动失败，进程提前退出: ${log_file}" >&2
      return 1
    fi
    if curl -fsS --max-time 1 "${BASE_URL}/" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.25
  done
  echo "服务启动超时: ${log_file}" >&2
  return 1
}

sample_resources() {
  local pid="$1"
  local output="$2"
  local engine="$3"

  printf "timestamp_s,engine,cpu_percent,rss_mb\n" >"${output}"
  while kill -0 "${pid}" 2>/dev/null; do
    local line cpu rss_kb rss_mb
    line="$(ps -p "${pid}" -o %cpu= -o rss= 2>/dev/null | awk 'NR==1 {print $1","$2}')"
    if [[ -n "${line}" ]]; then
      IFS=',' read -r cpu rss_kb <<<"${line}"
      rss_mb="$(awk -v r="${rss_kb}" 'BEGIN {printf "%.3f", r/1024}')"
      printf "%s,%s,%s,%s\n" "$(date +%s)" "${engine}" "${cpu}" "${rss_mb}" >>"${output}"
    fi
    sleep "${SAMPLE_INTERVAL}"
  done
}

request_once() {
  local engine="$1"
  local round="$2"
  local route="$3"
  local seq="$4"
  local temp_dir="$5"

  local url out_file err_file now_s rc metrics
  url="${BASE_URL}${route}"
  out_file="${temp_dir}/${seq}.csv"
  err_file="${temp_dir}/${seq}.err"
  now_s="$(date +%s)"

  rc=0
  metrics="$(curl -sS -L -o /dev/null --max-time "${MAX_TIME}" \
    -w "%{http_code},%{time_total},%{time_connect},%{time_starttransfer},%{size_download}" \
    "${url}" 2>"${err_file}")" || rc=$?

  local status http_code total_s connect_s ttfb_s size_bytes err_kind err_msg
  status="ok"
  http_code=0
  total_s=0
  connect_s=0
  ttfb_s=0
  size_bytes=0
  err_kind=""
  err_msg=""

  if [[ "${rc}" -eq 0 ]]; then
    IFS=',' read -r http_code total_s connect_s ttfb_s size_bytes <<<"${metrics}"
    if [[ "${http_code}" -ge 500 ]]; then
      status="fail_http"
      err_kind="http"
      err_msg="HTTP_${http_code}"
    fi
  else
    status="fail_conn"
    if [[ "${rc}" -eq 28 ]]; then
      status="timeout"
      err_kind="timeout"
    else
      err_kind="curl_${rc}"
    fi
    if [[ -s "${err_file}" ]]; then
      err_msg="$(tr '\n' ' ' <"${err_file}" | sed -E 's/[[:space:]]+/ /g; s/,/;/g; s/^ //; s/ $//')"
    fi
  fi

  rm -f "${err_file}"
  printf "%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s\n" \
    "${seq}" "${now_s}" "${engine}" "${round}" "${route}" "${status}" "${http_code}" \
    "${total_s}" "${connect_s}" "${ttfb_s}" "${size_bytes}" "${err_kind}" "${err_msg}" \
    >"${out_file}"
}

merge_requests() {
  local temp_dir="$1"
  local output="$2"

  printf "timestamp_s,engine,round,route,status,http_code,total_s,connect_s,ttfb_s,size_bytes,err_kind,err_msg\n" >"${output}"

  shopt -s nullglob
  local files=("${temp_dir}"/*.csv)
  shopt -u nullglob
  if [[ "${#files[@]}" -eq 0 ]]; then
    return 0
  fi

  cat "${files[@]}" | sort -t, -k1,1n | cut -d, -f2- >>"${output}"
}

calc_latency_stats_ms() {
  local req_file="$1"
  local lat_file count

  lat_file="$(mktemp "${OUT_DIR}/lat.XXXXXX")"
  awk -F, 'NR>1 && $5=="ok" {print $7+0}' "${req_file}" | sort -n >"${lat_file}"
  count="$(wc -l <"${lat_file}" | tr -d ' ')"

  if [[ "${count}" -eq 0 ]]; then
    rm -f "${lat_file}"
    echo "0 0 0 0 0 0"
    return 0
  fi

  local avg std p50 p95 p99 max
  avg="$(awk '{s+=$1} END {printf "%.3f", (s/NR)*1000}' "${lat_file}")"
  std="$(awk '{s+=$1; ss+=$1*$1} END {m=s/NR; v=(ss/NR)-(m*m); if(v<0)v=0; printf "%.3f", sqrt(v)*1000}' "${lat_file}")"
  p50="$(awk -v n="${count}" 'NR==int(0.50*n + 0.999999) {printf "%.3f", $1*1000; exit}' "${lat_file}")"
  p95="$(awk -v n="${count}" 'NR==int(0.95*n + 0.999999) {printf "%.3f", $1*1000; exit}' "${lat_file}")"
  p99="$(awk -v n="${count}" 'NR==int(0.99*n + 0.999999) {printf "%.3f", $1*1000; exit}' "${lat_file}")"
  max="$(tail -n 1 "${lat_file}" | awk '{printf "%.3f", $1*1000}')"

  rm -f "${lat_file}"
  echo "${avg} ${p50} ${p95} ${p99} ${max} ${std}"
}

calc_resource_stats() {
  local sample_file="$1"
  local cpu_avg cpu_peak rss_avg rss_peak rss_delta

  cpu_avg="$(awk -F, 'NR>1 {s+=$3; n++} END {if(n==0) printf "0.000"; else printf "%.3f", s/n}' "${sample_file}")"
  cpu_peak="$(awk -F, 'NR>1 {if($3>m) m=$3} END {printf "%.3f", m+0}' "${sample_file}")"
  rss_avg="$(awk -F, 'NR>1 {s+=$4; n++} END {if(n==0) printf "0.000"; else printf "%.3f", s/n}' "${sample_file}")"
  rss_peak="$(awk -F, 'NR>1 {if($4>m) m=$4} END {printf "%.3f", m+0}' "${sample_file}")"
  rss_delta="$(awk -F, 'NR==2 {first=$4} NR>1 {last=$4} END {if(first=="" || last=="") printf "0.000"; else printf "%.3f", last-first}' "${sample_file}")"

  echo "${cpu_avg} ${cpu_peak} ${rss_avg} ${rss_peak} ${rss_delta}"
}

summarize_engine() {
  local engine="$1"
  local req_file="$2"
  local sample_file="$3"

  local total ok fail_http timeout fail_conn success_rate
  total="$(awk -F, 'NR>1 {n++} END {print n+0}' "${req_file}")"
  ok="$(awk -F, 'NR>1 && $5=="ok" {n++} END {print n+0}' "${req_file}")"
  fail_http="$(awk -F, 'NR>1 && $5=="fail_http" {n++} END {print n+0}' "${req_file}")"
  timeout="$(awk -F, 'NR>1 && $5=="timeout" {n++} END {print n+0}' "${req_file}")"
  fail_conn="$(awk -F, 'NR>1 && $5=="fail_conn" {n++} END {print n+0}' "${req_file}")"
  success_rate="$(awk -v ok="${ok}" -v total="${total}" 'BEGIN {if(total==0) printf "0.000"; else printf "%.3f", (ok/total)*100}')"

  local lat_avg lat_p50 lat_p95 lat_p99 lat_max lat_std
  read -r lat_avg lat_p50 lat_p95 lat_p99 lat_max lat_std <<<"$(calc_latency_stats_ms "${req_file}")"

  local cpu_avg cpu_peak rss_avg rss_peak rss_delta
  read -r cpu_avg cpu_peak rss_avg rss_peak rss_delta <<<"$(calc_resource_stats "${sample_file}")"

  printf "%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s\n" \
    "${engine}" "${total}" "${ok}" "${fail_http}" "${timeout}" "${fail_conn}" "${success_rate}" \
    "${lat_avg}" "${lat_p50}" "${lat_p95}" "${lat_p99}" "${lat_max}" "${lat_std}" \
    "${cpu_avg}" "${cpu_peak}" "${rss_avg}" "${rss_peak}" "${rss_delta}" "${TOTAL_EXPECTED}" \
    >>"${SUMMARY_CSV}"
}

run_engine() {
  local engine="$1"
  local req_file="$2"
  local sample_file="$3"
  local log_file="$4"

  local temp_dir
  temp_dir="$(mktemp -d "${OUT_DIR}/req_${engine}.XXXXXX")"

  echo "启动引擎: ${engine}"
  if [[ -n "${RENDER_LIMIT}" ]]; then
    SSR_ENGINE="${engine}" SSR_RENDER_LIMIT="${RENDER_LIMIT}" "${BIN_PATH}" >"${log_file}" 2>&1 &
  else
    SSR_ENGINE="${engine}" "${BIN_PATH}" >"${log_file}" 2>&1 &
  fi
  SERVER_PID="$!"

  if ! wait_server_ready "${SERVER_PID}" "${log_file}"; then
    cat "${log_file}" >&2 || true
    exit 1
  fi

  sample_resources "${SERVER_PID}" "${sample_file}" "${engine}" &
  SAMPLER_PID="$!"

  local seq=0
  local round route i pid
  for ((round=1; round<=ROUNDS; round++)); do
    for route in "${ROUTES[@]}"; do
      local pids=()
      for ((i=1; i<=CONCURRENCY; i++)); do
        seq=$((seq + 1))
        request_once "${engine}" "${round}" "${route}" "${seq}" "${temp_dir}" &
        pids+=("$!")
      done
      for pid in "${pids[@]}"; do
        wait "${pid}"
      done
    done
  done

  if [[ -n "${SAMPLER_PID}" ]] && kill -0 "${SAMPLER_PID}" 2>/dev/null; then
    kill "${SAMPLER_PID}" >/dev/null 2>&1 || true
    wait "${SAMPLER_PID}" >/dev/null 2>&1 || true
  fi
  SAMPLER_PID=""

  if [[ -n "${SERVER_PID}" ]] && kill -0 "${SERVER_PID}" 2>/dev/null; then
    kill "${SERVER_PID}" >/dev/null 2>&1 || true
    wait "${SERVER_PID}" >/dev/null 2>&1 || true
  fi
  SERVER_PID=""

  merge_requests "${temp_dir}" "${req_file}"
  rm -rf "${temp_dir}"
  summarize_engine "${engine}" "${req_file}" "${sample_file}"
}

compare_winner() {
  awk -F, '
NR==1 {next}
{
  engine=$1
  success=$7+0
  p95=$10+0
  rss=$17+0

  if (!seen || success>best_success || (success==best_success && p95<best_p95) || (success==best_success && p95==best_p95 && rss<best_rss)) {
    seen=1
    best_engine=engine
    best_success=success
    best_p95=p95
    best_rss=rss
  }
}
END {print best_engine}
' "${SUMMARY_CSV}"
}

echo "构建 example 可执行文件..."
(
  cd "${ROOT_DIR}"
  go build -o "${BIN_PATH}" ./example
)

printf "engine,total,ok,fail_http,timeout,fail_conn,success_rate,lat_avg_ms,lat_p50_ms,lat_p95_ms,lat_p99_ms,lat_max_ms,lat_std_ms,cpu_avg_percent,cpu_peak_percent,rss_avg_mb,rss_peak_mb,rss_delta_mb,expected_requests\n" >"${SUMMARY_CSV}"

echo "开始压测: routes=${#ROUTES[@]}, rounds=${ROUNDS}, concurrency=${CONCURRENCY}, expected=${TOTAL_EXPECTED}"
run_engine "v8" "${REQ_V8}" "${SAMPLE_V8}" "${LOG_V8}"
run_engine "goja" "${REQ_GOJA}" "${SAMPLE_GOJA}" "${LOG_GOJA}"

WINNER="$(compare_winner)"

{
  echo "SSR 引擎压测报告"
  echo "generated_at: $(date +%Y-%m-%dT%H:%M:%S%z)"
  echo "base_url: ${BASE_URL}"
  echo "route_count: ${#ROUTES[@]}"
  echo "rounds: ${ROUNDS}"
  echo "concurrency: ${CONCURRENCY}"
  echo "max_time_sec: ${MAX_TIME}"
  echo "include_slow: ${INCLUDE_SLOW}"
  if [[ -n "${RENDER_LIMIT}" ]]; then
    echo "ssr_render_limit: ${RENDER_LIMIT}"
  fi
  echo
  echo "summary.csv:"
  cat "${SUMMARY_CSV}"
  echo
  echo "winner: ${WINNER}"
  echo "规则: success_rate 高者优先，若相同看 p95，更低者优先；再看 rss_peak。"
  echo
  echo "输出文件:"
  echo "  ${SUMMARY_CSV}"
  echo "  ${REPORT_TXT}"
  echo "  ${REQ_V8}"
  echo "  ${REQ_GOJA}"
  echo "  ${SAMPLE_V8}"
  echo "  ${SAMPLE_GOJA}"
  echo "  ${LOG_V8}"
  echo "  ${LOG_GOJA}"
} >"${REPORT_TXT}"

cat "${REPORT_TXT}"
