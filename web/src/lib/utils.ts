import { clsx, type ClassValue } from 'clsx'
import { twMerge } from 'tailwind-merge'

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

const sampColorPattern = /\{([0-9a-fA-F]{6}(?:[0-9a-fA-F]{2})?)\}/g

export interface ColoredTextSegment {
  text: string
  color?: string
}

export function parseSAMPColors(text: string): ColoredTextSegment[] {
  const segments: ColoredTextSegment[] = []
  let color: string | undefined
  let offset = 0
  for (const match of text.matchAll(sampColorPattern)) {
    if (match.index > offset) segments.push({ text: text.slice(offset, match.index), color })
    color = `#${match[1]}`
    offset = match.index + match[0].length
  }
  if (offset < text.length) segments.push({ text: text.slice(offset), color })
  return segments.length ? segments : [{ text }]
}

export function stripSAMPColors(text: string): string {
  return text.replace(sampColorPattern, '')
}
