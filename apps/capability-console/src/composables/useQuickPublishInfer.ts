import { computed, ref, watch } from 'vue';
import type { ComputedRef, Ref } from 'vue';
import { ElMessage } from 'element-plus';
import { inferQuickPublish } from '../api';
import type { HttpMethod, QuickPublishPayload } from '../types';

export type QuickPublishInferField = 'name' | 'domain' | 'resource_type' | 'summary_template';

export interface UseQuickPublishInferOptions {
  baseURL: Ref<string>;
  path: Ref<string>;
  description: Ref<string>;
  method: Ref<HttpMethod>;
  /** 自动推断字段（对应后端 inferred 返回值）。 */
  name: Ref<string>;
  domain: Ref<string>;
  resourceType: Ref<string>;
  summaryTemplate: Ref<string>;
}

export interface QuickPublishInfer {
  inferring: Ref<boolean>;
  /** 是否完成过一次成功推断（用于 UI 显示"已补全"与占位符）。 */
  hasInferred: Ref<boolean>;
  /** 最近一次成功推断补全的字段数（0 表示已全部由用户填写）。 */
  inferredCount: Ref<number>;
  canInfer: ComputedRef<boolean>;
  doInfer: () => Promise<void>;
  /** 标记某推断字段被用户手动编辑过，推断时不再覆盖它。 */
  markUserEdited: (field: QuickPublishInferField) => void;
  /** 发布成功后重置状态（清空推断与字段保护标记）。 */
  reset: () => void;
}

/**
 * useQuickPublishInfer 封装快速发布表单的 AI 字段补全：
 * - 三个必填项首次填齐时自动补全一次（避免每次修改都请求），
 *   "AI 一键补全 / 重新补全"按钮仍可随时手动触发；
 * - 只覆盖用户未手动编辑过的字段（字段级脏标记），避免推断覆盖用户手填内容；
 * - 失败用 ElMessage.warning 提示而非静默降级。
 */
export function useQuickPublishInfer(options: UseQuickPublishInferOptions): QuickPublishInfer {
  const { baseURL, path, description, method, name, domain, resourceType, summaryTemplate } = options;

  const inferring = ref(false);
  const hasInferred = ref(false);
  const inferredCount = ref(0);
  // 字段级脏标记：用户手动编辑过则推断不覆盖。
  const userEdited = ref<Set<QuickPublishInferField>>(new Set());
  // 自动补全的沿触发标志：必填项从"未齐"变为"齐"且尚未成功推断时才自动触发。
  let autoTriggered = false;

  const canInfer = computed(
    () =>
      !inferring.value &&
      baseURL.value.trim() !== '' &&
      path.value.trim() !== '' &&
      description.value.trim() !== '',
  );

  // 必填项首次全部填齐（path 与 description 从缺变为有）时自动补全一次。
  // 手动编辑必填项不会反复触发；已推断后如需更新，走手动"重新补全"按钮。
  watch(
    [path, description],
    ([nextPath, nextDescription], [prevPath, prevDescription]) => {
      const nowReady = nextPath.trim() !== '' && nextDescription.trim() !== '';
      const wasReady = prevPath.trim() !== '' && prevDescription.trim() !== '';
      if (nowReady && !wasReady && !autoTriggered) {
        autoTriggered = true;
        void doInfer();
      }
    },
    { flush: 'sync' },
  );

  function buildPayload(): QuickPublishPayload {
    const payload: QuickPublishPayload = {
      name: name.value.trim(),
      domain: domain.value.trim(),
      resource_type: resourceType.value.trim(),
      backend_base_url: baseURL.value.trim(),
      method: method.value,
      path: path.value.trim(),
      description: description.value.trim(),
    };
    if (summaryTemplate.value.trim() !== '') {
      payload.summary_template = summaryTemplate.value.trim();
    }
    return payload;
  }

  async function doInfer(): Promise<void> {
    if (!canInfer.value) return;
    inferring.value = true;
    try {
      const result = await inferQuickPublish(buildPayload());
      const inferred = result.inferred;
      let count = 0;
      const apply = (field: QuickPublishInferField, current: Ref<string>, value: string) => {
        if (userEdited.value.has(field)) {
          return;
        }
        const trimmed = value.trim();
        if (trimmed !== '' && current.value.trim() !== trimmed) {
          current.value = trimmed;
          count++;
        }
      };
      apply('name', name, inferred.name ?? '');
      apply('domain', domain, inferred.domain ?? '');
      apply('resource_type', resourceType, inferred.resource_type ?? '');
      apply('summary_template', summaryTemplate, inferred.summary_template ?? '');
      hasInferred.value = true;
      inferredCount.value = count;
      if (count > 0) {
        ElMessage.success(`AI 已补全 ${count} 个配置字段`);
      }
    } catch (err) {
      const message = err instanceof Error ? err.message : '智能推断失败';
      // 推断失败不阻塞发布，但明确提示用户可手动补全。
      ElMessage.warning(`AI 补全失败：${message}，可手动填写`);
    } finally {
      inferring.value = false;
    }
  }

  function markUserEdited(field: QuickPublishInferField): void {
    userEdited.value.add(field);
  }

  function reset(): void {
    hasInferred.value = false;
    inferredCount.value = 0;
    userEdited.value = new Set();
    // 复位沿触发标志，使下一次"填齐必填项"仍能自动补全。
    autoTriggered = false;
  }

  return {
    inferring,
    hasInferred,
    inferredCount,
    canInfer,
    doInfer,
    markUserEdited,
    reset,
  };
}
