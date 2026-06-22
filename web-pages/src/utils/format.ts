export function formatUnix(seconds?: number | null) {
  if (!seconds) return ''
  return new Date(seconds * 1000).toLocaleString()
}
