import type { ManagedCapability } from './types';

export type UiCapabilityState = 'draft' | 'needs_validation' | 'publishable' | 'published';

export const UI_CAPABILITY_STATE_LABEL: Record<UiCapabilityState, string> = {
  draft: '草稿',
  needs_validation: '待验证',
  publishable: '可发布',
  published: '已发布',
};

export function uiStateLabel(state: UiCapabilityState): string {
  return UI_CAPABILITY_STATE_LABEL[state];
}

export function uiStateOf(
  capability: ManagedCapability,
  isPublishable: (capability: ManagedCapability) => boolean,
): UiCapabilityState {
  if (capability.source === 'published') {
    return 'published';
  }
  if (isPublishable(capability)) {
    return 'publishable';
  }
  if (capability.validation.valid) {
    return 'needs_validation';
  }
  return 'draft';
}
