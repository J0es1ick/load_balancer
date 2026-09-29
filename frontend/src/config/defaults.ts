import demoConfig from "./demo.gateway.json" with { type: "json" };
import type { GatewayConfig } from "../types.js";

export function createDemoConfig(): GatewayConfig {
  return cloneConfig(demoConfig as GatewayConfig);
}

export function cloneConfig(config: GatewayConfig): GatewayConfig {
  return JSON.parse(JSON.stringify(config)) as GatewayConfig;
}
