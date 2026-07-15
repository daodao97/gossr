import type { Component } from 'vue'

const layoutModules = import.meta.glob('../layouts/*.vue', {
  eager: true,
  import: 'default',
}) as Record<string, Component>

export const layouts = Object.fromEntries(
  Object.entries(layoutModules).map(([path, component]) => {
    const layoutName = path.split('/').pop()?.replace('.vue', '') ?? path
    return [layoutName, component]
  }),
) as Readonly<Record<string, Component>>
