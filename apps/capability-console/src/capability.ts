import type { Capability, ManagedCapability } from './types';

/**
 * BUILTIN_TOOL_NAMES 与后端 internal/tools/registry.go 中的静态工具常量保持一致。
 * 这些工具名在发布时会被 tools.Lookup 命中并返回 409 冲突，前端在发布前先做预检，
 * 给出友好的提示，避免请求到后端才失败。
 */
export const BUILTIN_TOOL_NAMES: readonly string[] = Object.freeze([
  'cluster.status.read',
  'topic.retention.set',
  'glusterfs.volume.health.read',
  'minio.bucket.health.read',
  'kafka.consumer_lag.read',
]);

/**
 * hasStaticToolConflict 判断给定 capability 名称是否与后端 internal/tools/registry.go
 * 中的静态工具冲突。发布时这些名字会被 tools.Lookup 命中并返回 409，前端在发布前先做
 * 预检，给出友好提示，避免请求到后端才失败。
 */
export function hasStaticToolConflict(name: string): boolean {
  return BUILTIN_TOOL_NAMES.includes(name);
}

export function emptyCapability(): Capability {
  return {
    schema_version: 1,
    name: '',
    status: 'needs_review',
    domain: '',
    resource_type: '',
    operation: 'read',
    risk: 'low',
    backend: {
      adapter: 'http',
      method: 'GET',
      path: '',
      timeout_ms: 3000,
      base_url: '',
    },
    input_schema: {
      environment: { type: 'string', required: true },
    },
    output: {
      kind: 'observation',
      severity_path: '',
      summary_template: '',
      fields: {},
    },
    auth: {
      roles: ['viewer', 'operator', 'admin'],
      environment_scoped: true,
    },
    ai: {
      description: '',
      examples: [],
    },
  };
}

export function normalizeCapability(raw: Partial<ManagedCapability>): ManagedCapability {
  const fallback = emptyCapability();
  return {
    ...fallback,
    ...raw,
    backend: { ...fallback.backend, ...raw.backend },
    input_schema: raw.input_schema ?? fallback.input_schema,
    output: { ...fallback.output, ...raw.output, fields: raw.output?.fields ?? fallback.output.fields },
    auth: { ...fallback.auth, ...raw.auth },
    ai: { ...fallback.ai, ...raw.ai },
    governance: raw.governance,
    source: raw.source ?? 'discovered',
    validation: raw.validation ?? { valid: false, error: '未校验' },
  };
}

export function pathVariables(path: string): string[] {
  const names = new Set<string>();
  for (const match of path.matchAll(/\{([a-zA-Z0-9_]+)\}/g)) {
    names.add(match[1]);
  }
  return Array.from(names);
}

export function canPublish(capability: ManagedCapability): boolean {
  if (capability.source !== 'discovered') {
    return false;
  }
  if (!capability.validation.valid) {
    return false;
  }
  if (capability.operation === 'read') {
    return capability.backend.method === 'GET';
  }
  // Write capabilities require a mutating HTTP method; governance is enforced
  // by the backend during Publish, not by this guard.
  return ['POST', 'PUT', 'PATCH', 'DELETE'].includes(capability.backend.method);
}
