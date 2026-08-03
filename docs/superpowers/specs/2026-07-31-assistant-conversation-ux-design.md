# 助手对话交互优化（第一批）

- **日期**：2026-07-31
- **范围**：`apps/capability-console` 前端
- **目标**：解决对话交互的三个体验痛点——流式无光标、跨天消息无分组、错误丢失上下文
- **非目标**：导航与信息架构、空状态与首次引导属后续批次

## 背景

当前对话界面已具备 SSE 流式输出、反馈按钮、重试、typing 动画、响应类型 badge 等基础能力，但存在三处影响体验的细节断档：

1. 流式开启时，`useAssistant` 会向 `conversationTurns` 推入一个空 content 的 streaming turn，同时 `AssistantTranscript` 在 `loading` 时仍渲染独立 typing 行，两者重复；首字到达前是空气泡，首字到达后 typing 行还在，视觉断档。
2. 每条消息单独显示相对时间，跨天跳变突兀，无法快速定位「昨天的某次对话」。
3. `handleStreamError` 删除了 `optimisticUserTurn` 和 `streamingTurn`，用户消息消失，错误只显在顶部红条，看不到「我问了什么→AI 失败了」的上下文。

## 设计

### 1. 流式光标与过渡

**问题根因**：`AssistantTranscript` 的 typing 行与 streaming turn 重复渲染。

**方案**：

- `AssistantTranscript` 增加判断：当最后一个 turn 是 streaming turn（`turn.id` 以 `local-assistant-` 开头且 `props.loading === true`）时，**不再渲染独立 typing 行**，改由 `ConversationTurnItem` 内部处理过渡态。
- `ConversationTurnItem` 新增 `streaming?: boolean` prop：
  - `streaming && content === ''`：气泡内显示三点 typing 动画（复用 `AssistantTranscript` 已有的 `.typing-dots` 样式，提取到共享位置或复制定义）
  - `streaming && content !== ''`：内容末尾追加闪烁光标 `▌`（CSS `@keyframes blink` 动画，0.8s 一次 opacity 切换）
- 非流式模式（`streamingEnabled=false`，测试用）仍保留独立 typing 行。
- `AssistantTranscript` 需把 `streaming` prop 传给最后一个 turn：当 `loading && turns.length > 0 && turns[last].id.startsWith('local-assistant-')` 时传 `true`。

**接口变更**：

```typescript
// ConversationTurnItem.vue props 新增
defineProps<{
  turn: ConversationTurn;
  isLast?: boolean;
  streaming?: boolean; // 新增
}>();
```

**交互细节**：
- 光标颜色与内容文字一致（`currentColor`）
- 流式完成后 `streaming` 变为 `false`，光标消失
- 测试用 `setStreamingEnabled(false)` 路径不受影响

### 2. 消息时间分组

**问题根因**：每条消息独立显示相对时间，跨天对话难定位。

**方案**：

- `conversationFormat.ts` 新增导出函数：

```typescript
export interface DateGroup {
  key: string;       // YYYY-MM-DD，用于去重
  label: string;     // 显示文案：今天 / 昨天 / M月D日 / YYYY年M月D日
}

export function formatDateGroup(iso: string, now: Date = new Date()): DateGroup;
```

  标签规则：
  - 同一天 → 「今天」
  - 前一天 → 「昨天」
  - 同年 → 「M月D日」
  - 跨年 → 「YYYY年M月D日」

- `AssistantTranscript` 计算每个 turn 的日期分组 key，当 key 变化时在 turns 列表插入一条分隔线（`<div class="conversation-date-divider">`）。
- 分隔线样式：细线（`1px solid var(--color-border)`）+ 居中日期胶囊（背景 `var(--color-bg-elevated)`，圆角 `pill`，字号 `font-xs`）。
- 分隔线用 `position: sticky; top: 0; z-index: 1`，滚动时当前日期标签悬浮，方便定位。
- 加载更多历史时，新的分隔线按相同规则生成。

**渲染结构**：

```vue
<template v-for="(turn, index) in turns" :key="turn.id">
  <div v-if="shouldShowDivider(index)" class="conversation-date-divider">
    <span>{{ getDateGroupLabel(turn.created_at) }}</span>
  </div>
  <ConversationTurnItem :turn="turn" :is-last="index === turns.length - 1" />
</template>
```

`shouldShowDivider(index)`：`index === 0 || dateKey(turns[index].created_at) !== dateKey(turns[index-1].created_at)`。

### 3. 错误上下文保留

**问题根因**：`handleStreamError` 删除了 optimistic user turn 和 streaming turn，用户消息消失。

**方案**：

- `ConversationTurn` 类型新增可选字段 `error?: boolean`：

```typescript
export interface ConversationTurn {
  // ... 现有字段
  error?: boolean;
}
```

- `useAssistant.handleStreamError` 修改：
  - **不再删除** `optimisticUserTurn`：保留用户消息。
  - `streamingTurn` 的 `content` 替换为错误信息文案（如 `AI 助手请求失败：${msg}`），并设置 `error: true`。
  - `lastFailedAssistantMessage` 仍记录原用户消息，供重试使用。

- `ConversationTurnItem` 检测 `turn.error`：
  - 渲染红色气泡（`background: var(--color-danger-soft); border-color: var(--color-danger)`）
  - 内容前加错误图标（复用 App.vue 已有的 `assistant-error-icon` SVG）
  - 气泡下方显示「重试」按钮，触发 `emit('retry')`（新增 emit）

- `AssistantTranscript` 新增 `retry` emit 转发，`App.vue` 接收后调用 `useAssistant.retry()`。

- 下次成功发送或重试成功时：
  - `submitAssistantMessage` 开始时清理所有 `error: true` 的 turn，保持对话干净，避免错误气泡堆积。
  - 重试按钮固定在最后一条错误气泡上，用户点击重试后该气泡被新的 streaming turn 替换。
  - 用户若需回看历史错误，可去审计记录查看。

**接口变更**：

```typescript
// ConversationTurnItem.vue emits 新增
defineEmits<{
  (event: 'copy', content: string): void;
  (event: 'regenerate', turn: ConversationTurn): void;
  (event: 'retry'): void; // 新增
}>();
```

**顶部红条调整**：
- 保留作为全局状态指示
- 内容改为简短提示：「AI 响应失败，详见对话」+ 「重试」按钮（仍调用 `retryLastAssistantMessage`）
- 不再显示完整错误堆栈

## 组件改动清单

| 文件 | 改动 |
|------|------|
| `src/types.ts` | `ConversationTurn` 新增 `error?: boolean` |
| `src/conversationFormat.ts` | 新增 `formatDateGroup` 函数 |
| `src/conversationFormat.test.ts` | 新增 `formatDateGroup` 测试 |
| `src/composables/useAssistant.ts` | `handleStreamError` 保留上下文；`submitAssistantMessage` 清理错误 turn |
| `src/components/AssistantTranscript.vue` | streaming turn 时不渲染独立 typing 行；插入日期分隔线；新增 `retry` emit |
| `src/components/AssistantTranscript.test.ts` | 新增 streaming 光标、日期分组、错误 turn 测试 |
| `src/components/ConversationTurnItem.vue` | 新增 `streaming` prop（光标/typing）；`error` 渲染（红气泡+重试）；新增 `retry` emit |
| `src/components/ConversationTurnItem.test.ts` | 新增 streaming、error 状态测试 |
| `src/App.vue` | 接收 `retry` emit 转发到 `useAssistant.retry`；顶部红条文案调整 |
| `src/styles.css` | 新增 `.conversation-date-divider`、`.streaming-cursor`、`.conversation-turn-item.error` 样式 |

## 测试策略

- **单元测试**：
  - `conversationFormat.test.ts`：`formatDateGroup` 覆盖今天/昨天/同年/跨年边界
  - `AssistantTranscript.test.ts`：streaming 时光标渲染、非流式时独立 typing 行、日期分隔线出现位置
  - `ConversationTurnItem.test.ts`：streaming 空内容显示 typing、有内容显示光标、error turn 渲染红气泡+重试按钮
  - `useAssistant` 相关测试：`handleStreamError` 后 optimistic user turn 仍在、streamingTurn 标记 error、重试成功后错误 turn 清理
- **手动验证**：
  - SSE 流式发送消息，观察首字前 typing 动画、首字后光标闪烁、完成后光标消失
  - 跨天对话（修改本地时间或构造历史数据）观察日期分隔线
  - 断网或后端返回 500，观察错误气泡保留上下文 + 重试可用
  - 顶部红条文案是否简洁

## 风险与权衡

- **streaming turn 检测依赖 id 前缀 `local-assistant-`**：与 `useAssistant` 内部约定一致，已在该文件硬编码；若未来重构 id 生成需同步更新。
- **sticky 日期分隔线**：在 `assistant-transcript` 滚动容器内 `position: sticky` 需要 transcript 有 `overflow-y: auto`（已满足）。z-index 需低于 typing 行和 copy-notice，避免遮挡。
- **错误 turn 清理策略**：选择「重试时清理全部错误 turn」而非保留历史，避免错误气泡堆积干扰阅读；用户若需回看历史错误，可去审计记录查看。
- **非流式模式保留独立 typing 行**：保持测试稳定性，避免 `setStreamingEnabled(false)` 路径的快照失效。

## 验收标准

1. SSE 流式发送时，首字到达前显示三点 typing 动画（在 assistant 气泡内），首字到达后显示闪烁光标，流式完成后光标消失。
2. 非流式模式（`setStreamingEnabled(false)`）下，独立 typing 行行为不变。
3. 跨天对话显示日期分隔线，标签正确（今天/昨天/M月D日/YYYY年M月D日），滚动时当前日期标签 sticky 悬浮。
4. 流式出错时，用户消息保留，AI 气泡变红显示错误信息 + 重试按钮；顶部红条文案简洁。
5. 点击重试后，错误 turn 被新的 streaming turn 替换，重试成功后对话恢复正常状态。
6. 所有新增/修改的单元测试通过。
