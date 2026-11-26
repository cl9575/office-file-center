export type PreviewKind = 'image' | 'pdf' | 'text' | 'audio' | 'video'

const previewKinds: Record<string, PreviewKind> = {
  jpg: 'image',
  jpeg: 'image',
  png: 'image',
  webp: 'image',
  gif: 'image',
  bmp: 'image',
  avif: 'image',
  ico: 'image',
  pdf: 'pdf',
  txt: 'text',
  log: 'text',
  ini: 'text',
  csv: 'text',
  json: 'text',
  xml: 'text',
  md: 'text',
  markdown: 'text',
  mp3: 'audio',
  wav: 'audio',
  m4a: 'audio',
  ogg: 'audio',
  oga: 'audio',
  mp4: 'video',
  m4v: 'video',
  webm: 'video',
  ogv: 'video',
}

export function previewKindForName(name: string): PreviewKind | null {
  const dot = name.lastIndexOf('.')
  if (dot < 0) return null
  return previewKinds[name.slice(dot + 1).toLowerCase()] || null
}
