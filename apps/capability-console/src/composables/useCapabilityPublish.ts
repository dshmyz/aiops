import { computed, ref, watch } from 'vue';
import type { Ref } from 'vue';
import { ElMessage } from 'element-plus';
import { publishCapability, unpublishCapability } from '../api';
import { capabilityKey, upsert } from '../capabilityFormat';
import type { ManagedCapability } from '../types';
import type { ManagementPhase } from './useCapabilities';

/** 批量发布单条结果。 */
export interface PublishAllResult {
  success: number;
  failed: number;
  total: number;
  /** 失败明细（name → 原因），供 UI 展示，不再吞掉错误。 */
  failures: { name: string; reason: string }[];
}

export interface UseCapabilityPublishOptions {
  /** 能力列表（发布/下架后更新）。 */
  capabilities: Ref<ManagedCapability[]>;
  /** 共享错误提示，发布/下架失败时写入。 */
  error: Ref<string>;
  /** 共享阶段，发布成功后切到 ai 阶段。 */
  managementPhase: Ref<ManagementPhase>;
  /** 当前选中项（由 editor 持有）。 */
  selected: Ref<ManagedCapability>;
  /** 发布预检（editor 提供）。 */
  publishReady: Ref<boolean>;
  /** 单个能力是否可发布（editor 提供：canPublish + 同名/内置工具冲突预检）。 */
  isPublishable: (capability: ManagedCapability) => boolean;
  /** 发布/下架/导入后选中最新版本。 */
  onSelect: (capability: ManagedCapability) => void;
  /** 快速发布成功的提示消息。 */
  onQuickPublished: (message: string) => void;
  /** 批量发布完成后刷新列表。 */
  onRefresh: () => Promise<void>;
  /** 被忽略导入项的唯一键集合（import composable 提供）。 */
  ignoredKeys: Ref<Set<string>>;
}

/**
 * useCapabilityPublish 封装能力清单与发布生命周期：列表筛选/分页/状态统计，
 * 单条发布/下架、批量发布。能力级预检（isPublishable/publishReady）由 editor 提供，
 * 本 composable 专注列表级动作，通过 options 显式注入依赖。
 */
export function useCapabilityPublish(options: UseCapabilityPublishOptions) {
  const {
    capabilities,
    error,
    managementPhase,
    selected,
    publishReady,
    isPublishable,
    onSelect,
    onQuickPublished,
    onRefresh,
    ignoredKeys,
  } = options;

  const searchText = ref('');
  const statusFilter = ref('all');
  const domainFilter = ref('all');
  const pageSize = ref(20);
  const currentPage = ref(1);

  const availableDomains = computed(() => {
    const domains = new Set(capabilities.value.map((item) => item.domain).filter(Boolean));
    return Array.from(domains).sort();
  });

  const filteredCapabilities = computed(() => {
    const query = searchText.value.trim().toLowerCase();
    return capabilities.value.filter((item) => {
      if (ignoredKeys.value.has(capabilityKey(item))) {
        return false;
      }
      const matchesQuery =
        query === '' ||
        [item.name, item.domain, item.resource_type, item.backend.method, item.backend.path]
          .join(' ')
          .toLowerCase()
          .includes(query);
      const matchesStatus = statusFilter.value === 'all' || item.source === statusFilter.value || item.status === statusFilter.value;
      const matchesDomain = domainFilter.value === 'all' || item.domain === domainFilter.value;
      return matchesQuery && matchesStatus && matchesDomain;
    });
  });

  // 分页：过滤条件变化时重置页码
  watch([searchText, statusFilter, domainFilter], () => {
    currentPage.value = 1;
  });
  const paginatedCapabilities = computed(() => {
    const start = (currentPage.value - 1) * pageSize.value;
    return filteredCapabilities.value.slice(start, start + pageSize.value);
  });
  const totalPages = computed(() => Math.ceil(filteredCapabilities.value.length / pageSize.value));

  const publishedCapabilityNames = computed(
    () => new Set(capabilities.value.filter((item) => item.source === 'published').map((item) => item.name)),
  );
  const stats = computed(() => {
    const published = capabilities.value.filter((item) => item.source === 'published').length;
    const review = capabilities.value.filter((item) => item.status === 'needs_review' || item.source === 'discovered').length;
    const invalid = capabilities.value.filter((item) => !item.validation.valid).length;
    const publishable = capabilities.value.filter((item) => isPublishable(item)).length;
    return { published, review, invalid, publishable };
  });

  // 状态分组统计
  const groupedStats = computed(() => {
    const groups: Record<string, number> = { draft: 0, review: 0, published: 0, other: 0 };
    for (const item of capabilities.value) {
      if (item.source === 'published') groups.published++;
      else if (item.status === 'needs_review' || item.source === 'discovered') groups.review++;
      else groups.draft++;
    }
    return groups;
  });

  async function publishSelected(capability: ManagedCapability) {
    if (capability.name === selected.value.name && !publishReady.value) {
      return;
    }
    if (capability.name !== selected.value.name && !isPublishable(capability)) {
      return;
    }
    error.value = '';
    try {
      const published = await publishCapability(capability.name);
      capabilities.value = capabilities.value.filter((item) => item.name !== capability.name);
      capabilities.value.push(published);
      onSelect(published);
      managementPhase.value = 'ai';
      ElMessage.success(`已发布 ${published.name}`);
    } catch (err) {
      const msg = err instanceof Error ? err.message : '发布 Capability 失败';
      error.value = msg;
      ElMessage.error(msg);
    }
  }

  async function publishCurrent() {
    await publishSelected(selected.value);
  }

  async function unpublishSelected(capability: ManagedCapability) {
    error.value = '';
    try {
      const unpublished = await unpublishCapability(capability.name);
      capabilities.value = capabilities.value.filter((item) => item.name !== capability.name);
      capabilities.value.push(unpublished);
      onSelect(unpublished);
      ElMessage.success(`已下线 ${capability.name}`);
    } catch (err) {
      const msg = err instanceof Error ? err.message : '下线 Capability 失败';
      error.value = msg;
      ElMessage.error(msg);
    }
  }

  function handleQuickPublished(capability: ManagedCapability) {
    upsert(capabilities, capability);
    onSelect(capability);
    managementPhase.value = 'ai';
    onQuickPublished(`快速发布成功：${capability.name}`);
  }

  function handleQuickPublishError(message: string) {
    error.value = message;
  }

  /** 批量发布：逐个发布当前可发布项，返回成功/失败计数与失败明细（不再吞掉错误）。 */
  async function publishAll(): Promise<PublishAllResult | undefined> {
    const publishable = filteredCapabilities.value.filter((item) => isPublishable(item));
    if (publishable.length === 0) {
      return undefined;
    }
    let success = 0;
    const failures: { name: string; reason: string }[] = [];
    for (const item of publishable) {
      try {
        await publishCapability(item.name);
        success++;
      } catch (err) {
        failures.push({ name: item.name, reason: err instanceof Error ? err.message : '发布失败' });
      }
    }
    await onRefresh();
    return { success, failed: failures.length, total: publishable.length, failures };
  }

  return {
    searchText,
    statusFilter,
    domainFilter,
    pageSize,
    currentPage,
    availableDomains,
    publishedCapabilityNames,
    stats,
    filteredCapabilities,
    paginatedCapabilities,
    totalPages,
    groupedStats,
    publishSelected,
    publishCurrent,
    unpublishSelected,
    handleQuickPublished,
    handleQuickPublishError,
    publishAll,
  };
}

export type UseCapabilityPublish = ReturnType<typeof useCapabilityPublish>;
