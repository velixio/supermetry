/** The only static reference to the palette: a loader, so cmdk stays out of the initial bundle. */
export const loadPalette = () => import('./CommandPalette').then((module) => ({ default: module.CommandPalette }))
