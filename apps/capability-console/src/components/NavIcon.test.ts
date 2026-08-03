import { describe, expect, it } from 'vitest';
import { mount } from '@vue/test-utils';

import NavIcon from './NavIcon.vue';

describe('NavIcon', () => {
  it('renders an inline svg with 1em size that follows currentColor', () => {
    const wrapper = mount(NavIcon, { props: { name: 'assistant' } });
    const svg = wrapper.find('svg');
    expect(svg.exists()).toBe(true);
    // Inline svg must inherit color so the active state tint applies.
    expect(svg.attributes('fill')).toBe('currentColor');
    // 1em keeps the icon aligned with the surrounding text baseline.
    expect(svg.attributes('width')).toBe('1em');
    expect(svg.attributes('height')).toBe('1em');
  });

  it.each([
    ['assistant'],
    ['management'],
    ['plans'],
    ['audit'],
    ['scheduled-tasks'],
  ] as const)('renders a known icon for name=%s', (name) => {
    const wrapper = mount(NavIcon, { props: { name } });
    expect(wrapper.find('svg').exists()).toBe(true);
    // Each icon must draw at least one shape so it is visually non-empty.
    expect(wrapper.find('svg [d], svg circle, svg rect, svg path').exists()).toBe(true);
  });

  it('falls back to a placeholder dot for unknown names instead of crashing', () => {
    // Cast to bypass the typed prop so we can verify the runtime fallback
    // path that protects against future icon renames.
    const wrapper = mount(NavIcon, { props: { name: 'does-not-exist' as never } });
    expect(wrapper.find('svg').exists()).toBe(true);
    expect(wrapper.find('svg circle').exists()).toBe(true);
  });
});
