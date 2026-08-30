import Editor from '@monaco-editor/react'

type YamlEditorProps = {
  value: string
  onChange?: (value: string) => void
  readOnly?: boolean
  height?: number | string
}

export function YamlEditor({ value, onChange, readOnly, height = 420 }: YamlEditorProps) {
  return (
    <div className="yaml-frame">
      <Editor
        height={height}
        language="yaml"
        theme="vs-dark"
        value={value}
        onChange={(next) => onChange?.(next ?? '')}
        options={{
          readOnly,
          minimap: { enabled: false },
          fontSize: 13,
          tabSize: 2,
          scrollBeyondLastLine: false,
          wordWrap: 'on',
          automaticLayout: true,
        }}
      />
    </div>
  )
}
