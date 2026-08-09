import { parseSAMPColors } from '@/lib/utils'

export function SAMPColoredText({ text }: { text: string }) {
  return parseSAMPColors(text).map((segment, index) => (
    <span key={`${index}-${segment.text}`} style={{ color: segment.color }}>
      {segment.text}
    </span>
  ))
}
