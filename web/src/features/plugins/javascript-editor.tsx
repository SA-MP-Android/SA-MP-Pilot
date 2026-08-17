import CodeMirror from '@uiw/react-codemirror'
import { javascript } from '@codemirror/lang-javascript'
import { syntaxHighlighting } from '@codemirror/language'
import { oneDark } from '@codemirror/theme-one-dark'
import { EditorView } from '@codemirror/view'
import { classHighlighter } from '@lezer/highlight'

const EDITOR_MIN_HEIGHT = '13rem'
const EDITOR_MAX_HEIGHT = '18rem'
const EDITOR_FONT_SIZE = '0.75rem'
const EDITOR_LINE_HEIGHT = '1.25rem'
const EDITOR_PADDING = '0.5rem 0.75rem'

const editorTheme = EditorView.theme({
  '&': {
    minHeight: EDITOR_MIN_HEIGHT,
    maxHeight: EDITOR_MAX_HEIGHT,
    backgroundColor: 'transparent',
    fontSize: EDITOR_FONT_SIZE,
  },
  '.cm-scroller': {
    overflow: 'auto',
    fontFamily:
      'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace',
    lineHeight: EDITOR_LINE_HEIGHT,
  },
  '.cm-content': {
    minHeight: EDITOR_MIN_HEIGHT,
    padding: EDITOR_PADDING,
  },
  '.cm-line': {
    padding: '0',
  },
  '.cm-gutters': {
    display: 'none',
  },
  '.cm-activeLine': {
    backgroundColor: 'transparent',
  },
})

const editorExtensions = [javascript(), oneDark, syntaxHighlighting(classHighlighter), editorTheme]

const basicSetup = {
  lineNumbers: false,
  foldGutter: false,
  highlightActiveLine: false,
  highlightActiveLineGutter: false,
  autocompletion: true,
}

interface JavaScriptEditorProps {
  value: string
  onChange: (value: string) => void
  ariaLabel: string
}

export function JavaScriptEditor({ value, onChange, ariaLabel }: JavaScriptEditorProps) {
  return (
    <div className="plugin-code-editor border-input focus-within:ring-ring overflow-hidden rounded-md border bg-transparent focus-within:ring-1">
      <CodeMirror
        value={value}
        height={EDITOR_MAX_HEIGHT}
        theme="none"
        extensions={editorExtensions}
        basicSetup={basicSetup}
        onChange={onChange}
        aria-label={ariaLabel}
      />
    </div>
  )
}
