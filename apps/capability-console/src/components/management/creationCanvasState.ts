import { ref } from 'vue';

// 创建画布的来源选择状态放在模块级：组件随向导阶段反复挂载/卸载，
// 状态放组件内会导致“回到连接接口就重新选来源”，必须持久保存。
export const creationSource = ref<'single' | 'openapi' | null>(null);

export function resetCreationCanvasState() {
  creationSource.value = null;
}
