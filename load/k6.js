import http from "k6/http";
import { check, sleep } from "k6";
import { Counter, Rate } from "k6/metrics";

const profile = __ENV.PROFILE || "smoke";
const target = __ENV.TARGET_URL || "http://host.docker.internal:8080";
const expectedBackendIDs = (__ENV.EXPECTED_BACKENDS || "")
  .split(",")
  .map((value) => value.trim())
  .filter(Boolean);

const successfulResponses = new Counter("successful_responses");
const throttledResponses = new Counter("throttled_responses");
const overloadedResponses = new Counter("overloaded_responses");
const unexpectedResponses = new Counter("unexpected_responses");
const successRate = new Rate("successful_response_rate");
const unexpectedRate = new Rate("unexpected_response_rate");
const backendHitCounters = Object.fromEntries(
  expectedBackendIDs.map((id) => [
    id,
    new Counter(`backend_hits_${id.replace(/[^a-zA-Z0-9_]/g, "_")}`),
  ]),
);
const backendHitThresholds = Object.fromEntries(
  expectedBackendIDs.map((id) => [
    `backend_hits_${id.replace(/[^a-zA-Z0-9_]/g, "_")}`,
    ["count>0"],
  ]),
);

const profiles = {
  smoke: {
    executor: "constant-vus",
    vus: Number(__ENV.VUS || 1),
    duration: __ENV.DURATION || "10s",
  },
  throughput: {
    executor: "constant-vus",
    vus: Number(__ENV.VUS || 25),
    duration: __ENV.DURATION || "2m",
  },
  load: {
    executor: "constant-vus",
    vus: Number(__ENV.VUS || 25),
    duration: __ENV.DURATION || "2m",
  },
  "rate-limit": {
    executor: "constant-vus",
    vus: Number(__ENV.VUS || 10),
    duration: __ENV.DURATION || "30s",
  },
  overload: {
    executor: "constant-vus",
    vus: Number(__ENV.VUS || 300),
    duration: __ENV.DURATION || "1m",
  },
  soak: {
    executor: "constant-vus",
    vus: Number(__ENV.VUS || 50),
    duration: __ENV.DURATION || "30m",
  },
  spike: {
    executor: "ramping-vus",
    startVUs: 0,
    stages: [
      { duration: "20s", target: 20 },
      { duration: "10s", target: 300 },
      { duration: "30s", target: 300 },
      { duration: "20s", target: 0 },
    ],
  },
  recovery: {
    executor: "constant-vus",
    vus: Number(__ENV.VUS || 1),
    duration: __ENV.DURATION || "15s",
  },
};

if (!profiles[profile]) {
  throw new Error(`Unknown PROFILE=${profile}`);
}

const successProfile = [
  "smoke",
  "throughput",
  "load",
  "soak",
  "recovery",
].includes(profile);
const rateLimitProfile = profile === "rate-limit";
const overloadProfile = ["overload", "spike"].includes(profile);
const throughputProfile = ["throughput", "load"].includes(profile);
const minimumRPS = Number(__ENV.MIN_RPS || 100);

const profileThresholds = successProfile
  ? { successful_response_rate: ["rate>0.99"], http_req_failed: ["rate<0.01"] }
  : rateLimitProfile
    ? {
        successful_responses: ["count>0"],
        throttled_responses: ["count>0"],
        overloaded_responses: ["count==0"],
      }
    : { successful_responses: ["count>0"], overloaded_responses: ["count>0"] };

export const options = {
  scenarios: { proxy: profiles[profile] },
  thresholds: {
    ...profileThresholds,
    ...(throughputProfile ? { http_reqs: [`rate>${minimumRPS}`] } : {}),
    ...backendHitThresholds,
    unexpected_response_rate: ["rate<0.01"],
    http_req_duration: ["p(95)<500", "p(99)<1000"],
    checks: ["rate>0.99"],
  },
};

export function setup() {
  const ready = http.get(`${target}/readyz`);
  if (ready.status !== 200) {
    throw new Error(`Target is not ready: HTTP ${ready.status}`);
  }
}

export default function () {
  const response = http.get(`${target}/`, {
    tags: { endpoint: "proxy-root", profile },
  });
  const status = response.status;
  const success = status === 200;
  const throttled = status === 429;
  const overloaded = status === 503;
  const backendID = response.headers["X-Balancer-Backend"];
  const expected = successProfile
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
  expectedBackendIDs.forEach((id) => {
    backendHitCounters[id].add(success && backendID === id ? 1 : 0);
  });

  check(response, {
    "status matches the selected profile": () => expected,
    "successful response identifies its backend": (result) =>
      !success || Boolean(result.headers["X-Balancer-Backend"]),
    "successful response uses the expected backend pool": () =>
      !success || expectedBackendIDs.length === 0 || expectedBackendIDs.includes(backendID),
    "attempt count is valid when present": (result) => {
      const raw = result.headers["X-Balancer-Attempts"];
      return !raw || (Number(raw) >= 1 && Number(raw) <= 2);
    },
  });
  sleep(
    Number(
      __ENV.SLEEP_SECONDS ||
        (profile === "smoke" || profile === "recovery" ? 0.25 : 0.01),
    ),
  );
}
