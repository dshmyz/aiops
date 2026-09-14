import { ref } from 'vue';

// 创建画布的阶段状态放在模块级：组件随向导阶段反复挂载/卸载，
// 状态放组件内会导致“回到连接接口就重新回答目标”，必须持久保存。
export const creationGoal = ref('');
export const creationGoalConfirmed = ref(false);
export const creationSource = ref<'single' | 'openapi' | null>(null);

export function confirmCreationGoal() {
  creationGoalConfirmed.value = true;
}

export function resetCreationCanvasState() {
  creationGoal.value = '';
  creationGoalConfirmed.value = false;
  creationSource.value = null;
}
