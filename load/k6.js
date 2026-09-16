import http from "k6/http";
import { check, sleep } from "k6";
import execution from "k6/execution";
import { Counter, Rate } from "k6/metrics";

const profile = __ENV.PROFILE || "smoke";
const target = (__ENV.TARGET_URL || "http://host.docker.internal:8080").replace(/\/$/, "");
const expectedBackendIDs = csv(__ENV.EXPECTED_BACKENDS);
const backendPatterns = parseObject(__ENV.BACKEND_PATTERNS_JSON, {});
const expectedStatuses = new Set(numbers(__ENV.EXPECTED_STATUSES || "200"));
const rate = positiveNumber(__ENV.RATE || __ENV.VUS, 25);
const timeUnit = __ENV.TIME_UNIT || "1s";
const duration = __ENV.DURATION || (profile === "soak" ? "30m" : "2m");
const preAllocatedVUs = positiveInteger(__ENV.PRE_ALLOCATED_VUS, Math.max(10, Math.ceil(rate)));
const maxVUs = positiveInteger(__ENV.MAX_VUS, Math.max(preAllocatedVUs, Math.ceil(rate * 4)));
const payload = loadPayload();
const defaultMethod = (__ENV.METHOD || "GET").toUpperCase();
const defaultHeaders = parseObject(__ENV.HEADERS_JSON, {});
const endpoints = loadEndpoints();

const successfulResponses = new Counter("successful_responses");
const throttledResponses = new Counter("throttled_responses");
const overloadedResponses = new Counter("overloaded_responses");
const unexpectedResponses = new Counter("unexpected_responses");
const recoveryFailures = new Counter("recovery_failures");
const successRate = new Rate("successful_response_rate");
const unexpectedRate = new Rate("unexpected_response_rate");
const backendHitCounters = Object.fromEntries(
  expectedBackendIDs.map((id) => [metricName("backend_hits", id), new Counter(metricName("backend_hits", id))]),
);

const arrivalRate = (selectedRate = rate, selectedDuration = duration) => ({
  executor: "constant-arrival-rate",
  rate: selectedRate,
  timeUnit,
  duration: selectedDuration,
  preAllocatedVUs,
  maxVUs,
  exec: "proxyRequest",
});

const scenarios = {
  smoke: {
    proxy: {
      executor: "shared-iterations",
      vus: positiveInteger(__ENV.VUS, 1),
      iterations: positiveInteger(__ENV.ITERATIONS, 20),
      maxDuration: __ENV.DURATION || "30s",
      exec: "proxyRequest",
    },
  },
  throughput: { proxy: arrivalRate() },
  load: { proxy: arrivalRate() },
  "rate-limit": { proxy: arrivalRate() },
  overload: { proxy: arrivalRate(positiveNumber(__ENV.RATE || __ENV.VUS, 1000), duration) },
  soak: { proxy: arrivalRate(positiveNumber(__ENV.RATE, 50), duration) },
  capacity: {
    proxy: {
      executor: "ramping-arrival-rate",
      startRate: positiveNumber(__ENV.START_RATE, Math.max(1, Math.floor(rate / 4))),
      timeUnit,
      preAllocatedVUs,
      maxVUs,
      stages: parseStages(__ENV.STAGES_JSON, [
        { duration: "1m", target: rate },
        { duration: "3m", target: rate },
        { duration: "1m", target: rate * 2 },
        { duration: "3m", target: rate * 2 },
      ]),
      exec: "proxyRequest",
    },
  },
  spike: {
    proxy: {
      executor: "ramping-arrival-rate",
      startRate: positiveNumber(__ENV.START_RATE, Math.max(1, Math.floor(rate / 10))),
      timeUnit,
      preAllocatedVUs,
      maxVUs,
      stages: parseStages(__ENV.STAGES_JSON, [
        { duration: "20s", target: rate },
        { duration: "10s", target: rate * 10 },
        { duration: "30s", target: rate * 10 },
        { duration: "20s", target: rate },
      ]),
      exec: "proxyRequest",
    },
  },
  recovery: {
    pressure: {
      executor: "ramping-arrival-rate",
      startRate: rate,
      timeUnit,
      preAllocatedVUs,
      maxVUs,
      stages: parseStages(__ENV.STAGES_JSON, [
        { duration: "20s", target: rate },
        { duration: "20s", target: rate * 10 },
        { duration: "20s", target: rate * 10 },
      ]),
      exec: "proxyRequest",
    },
    probe: {
      executor: "constant-arrival-rate",
      startTime: __ENV.RECOVERY_START_TIME || "1m",
      duration: __ENV.RECOVERY_DURATION || "30s",
      rate: positiveNumber(__ENV.RECOVERY_RATE, Math.max(1, Math.floor(rate / 10))),
      timeUnit,
      preAllocatedVUs: Math.max(1, Math.min(preAllocatedVUs, 20)),
      maxVUs: Math.max(2, Math.min(maxVUs, 50)),
      exec: "recoveryProbe",
    },
  },
};

if (!scenarios[profile]) throw new Error(`Unknown PROFILE=${profile}`);

const successProfile = ["smoke", "throughput", "load", "soak", "capacity"].includes(profile);
const rateLimitProfile = profile === "rate-limit";
const overloadProfile = ["overload", "spike", "recovery"].includes(profile);
const successThreshold = Number(__ENV.MIN_SUCCESS_RATE || 0.99);
const unexpectedThreshold = Number(__ENV.MAX_UNEXPECTED_RATE || 0.01);
const p95Limit = positiveNumber(__ENV.P95_MS, 500);
const p99Limit = positiveNumber(__ENV.P99_MS, 1000);
const maxDropped = Number(__ENV.MAX_DROPPED_ITERATIONS || 0);

const profileThresholds = successProfile
  ? { successful_response_rate: [`rate>${successThreshold}`], http_req_failed: [`rate<${1 - successThreshold}`] }
  : rateLimitProfile
    ? { successful_responses: ["count>0"], throttled_responses: ["count>0"], overloaded_responses: ["count==0"] }
    : overloadProfile
      ? { successful_responses: ["count>0"] }
      : {};

const backendHitThresholds = Object.fromEntries(
  expectedBackendIDs.map((id) => [metricName("backend_hits", id), ["count>0"]]),
);

export const options = {
  scenarios: scenarios[profile],
  thresholds: {
    ...profileThresholds,
    ...backendHitThresholds,
    unexpected_response_rate: [`rate<${unexpectedThreshold}`],
    http_req_duration: [`p(95)<${p95Limit}`, `p(99)<${p99Limit}`],
    checks: [`rate>${successThreshold}`],
    ...(maxDropped >= 0 ? { dropped_iterations: [`count<=${maxDropped}`] } : {}),
    ...(profile === "recovery" ? { recovery_failures: ["count==0"] } : {}),
  },
  discardResponseBodies: expectedBackendIDs.length === 0 && __ENV.DISCARD_RESPONSE_BODIES !== "false",
};

export function setup() {
  const ready = http.get(joinURL(target, __ENV.READY_PATH || "/readyz"), {
    tags: { endpoint: "readiness", profile },
  });
  if (ready.status !== 200) throw new Error(`Target is not ready: HTTP ${ready.status}`);
}

export function proxyRequest() {
  exercise(false);
}

export function recoveryProbe() {
  exercise(true);
}

function exercise(recovery) {
  const endpoint = endpoints[execution.scenario.iterationInTest % endpoints.length];
  const method = (endpoint.method || defaultMethod).toUpperCase();
  const body = endpoint.body === undefined ? payload : endpoint.body;
  const headers = { ...defaultHeaders, ...(endpoint.headers || {}) };
  if (body !== null && body !== undefined && !headers["Content-Type"] && !headers["content-type"]) {
    headers["Content-Type"] = __ENV.CONTENT_TYPE || "application/json";
  }
  const response = http.request(method, joinURL(target, endpoint.path), bodyFor(method, body), {
    headers,
    tags: { endpoint: endpoint.name, profile, phase: recovery ? "recovery" : "pressure" },
    timeout: __ENV.REQUEST_TIMEOUT || "30s",
  });
  const status = response.status;
  const success = expectedStatuses.has(status);
  const throttled = status === 429;
  const overloaded = status === 503;
  const backendID = identifyBackend(response);
  const expected = recovery
    ? success
    : successProfile
      ? success
      : rateLimitProfile
        ? success || throttled
        : success || overloaded;

  successfulResponses.add(success ? 1 : 0);
  throttledResponses.add(throttled ? 1 : 0);
  overloadedResponses.add(overloaded ? 1 : 0);
  unexpectedResponses.add(expected ? 0 : 1);
  successRate.add(success);
  unexpectedRate.add(!expected);
  if (recovery && !success) recoveryFailures.add(1);
  expectedBackendIDs.forEach((id) => {
    backendHitCounters[metricName("backend_hits", id)].add(success && backendID === id ? 1 : 0);
  });

  check(response, {
    "status matches the selected profile": () => expected,
    "distribution sample identifies its backend": () => !success || expectedBackendIDs.length === 0 || Boolean(backendID),
    "successful response uses the expected backend pool": () =>
      !success || expectedBackendIDs.length === 0 || expectedBackendIDs.includes(backendID),
  });

  const thinkTime = Number(__ENV.SLEEP_SECONDS || 0);
  if (thinkTime > 0) sleep(thinkTime);
}

export function handleSummary(data) {
  const reportPath = __ENV.SUMMARY_PATH || "capacity-report.json";
  const report = {
    schema_version: 1,
    generated_at: new Date().toISOString(),
    inputs: {
      profile,
      target,
      endpoints: endpoints.map(({ name, path, method }) => ({ name, path, method: method || defaultMethod })),
      rate,
      time_unit: timeUnit,
      duration,
      pre_allocated_vus: preAllocatedVUs,
      max_vus: maxVUs,
      expected_statuses: [...expectedStatuses],
      expected_backends: expectedBackendIDs,
      backend_pattern_ids: Object.keys(backendPatterns),
      payload_bytes: payload ? payload.length || payload.byteLength || null : 0,
      build_revision: __ENV.BUILD_REVISION || "unknown",
      environment: __ENV.TEST_ENVIRONMENT || "unspecified",
      generator: __ENV.GENERATOR_ID || "unspecified",
    },
    result: data,
  };
  return {
    [reportPath]: JSON.stringify(report, null, 2),
    stdout: `k6 ${profile} complete; raw reproducibility report: ${reportPath}\n`,
  };
}

function loadEndpoints() {
  if (__ENV.ENDPOINTS_JSON) {
    const parsed = JSON.parse(__ENV.ENDPOINTS_JSON);
    if (!Array.isArray(parsed) || parsed.length === 0) throw new Error("ENDPOINTS_JSON must be a non-empty array");
    return parsed.map((entry, index) => ({
      name: entry.name || `endpoint-${index + 1}`,
      path: entry.path || "/",
      method: entry.method,
      headers: entry.headers,
      body: entry.body,
    }));
  }
  return csv(__ENV.ENDPOINTS || "/").map((path, index) => ({ name: `endpoint-${index + 1}`, path }));
}

function loadPayload() {
  if (__ENV.PAYLOAD_FILE) return open(__ENV.PAYLOAD_FILE, "b");
  if (__ENV.PAYLOAD !== undefined) return __ENV.PAYLOAD;
  const bytes = Number(__ENV.PAYLOAD_BYTES || 0);
  if (bytes < 0 || bytes > 10 * 1024 * 1024) throw new Error("PAYLOAD_BYTES must be between 0 and 10485760");
  return bytes === 0 ? null : "x".repeat(bytes);
}

function parseStages(raw, fallback) {
  if (!raw) return fallback;
  const stages = JSON.parse(raw);
  if (!Array.isArray(stages) || stages.length === 0) throw new Error("STAGES_JSON must be a non-empty array");
  return stages;
}

function parseObject(raw, fallback) {
  if (!raw) return fallback;
  const value = JSON.parse(raw);
  if (!value || Array.isArray(value) || typeof value !== "object") throw new Error("HEADERS_JSON must be an object");
  return value;
}

function bodyFor(method, value) {
  return ["GET", "HEAD", "OPTIONS"].includes(method) && value === null ? null : value;
}

function identifyBackend(response) {
  const marker = response.headers["X-Test-Backend"];
  if (marker) return marker;
  const body = typeof response.body === "string" ? response.body : "";
  for (const [id, pattern] of Object.entries(backendPatterns)) {
    if (body.includes(pattern)) return id;
  }
  return "";
}

function joinURL(base, path) {
  if (/^https?:\/\//.test(path)) return path;
  return `${base}${path.startsWith("/") ? "" : "/"}${path}`;
}

function csv(value) {
  return (value || "").split(",").map((item) => item.trim()).filter(Boolean);
}

function numbers(value) {
  return csv(value).map((item) => Number(item));
}

function positiveNumber(raw, fallback) {
  const value = Number(raw || fallback);
  if (!Number.isFinite(value) || value <= 0) throw new Error(`Expected a positive number, got ${raw}`);
  return value;
}

function positiveInteger(raw, fallback) {
  return Math.ceil(positiveNumber(raw, fallback));
}

function metricName(prefix, value) {
  return `${prefix}_${value.replace(/[^a-zA-Z0-9_]/g, "_")}`;
}
