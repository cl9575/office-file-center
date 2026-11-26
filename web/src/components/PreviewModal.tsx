import { useEffect, useState } from 'react'
import { Alert, Button, Modal, Spin } from 'antd'
import { DownloadOutlined } from '@ant-design/icons'
import { downloadUrl, previewUrl } from '../api'
import { previewKindForName } from '../preview'
import type { NodeItem } from '../types'

async function previewError(res: Response): Promise<string> {
  const payload = (await res.json().catch(() => null)) as
    | { error?: { message?: string } }
    | null
  return payload?.error?.message || `无法读取预览内容（${res.status}）`
}

export function PreviewModal({
  node,
  open,
  onClose,
}: {
  node: NodeItem | null
  open: boolean
  onClose: () => void
}) {
  const [text, setText] = useState('')
  const [truncated, setTruncated] = useState(false)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const kind = node ? previewKindForName(node.name) : null

  useEffect(() => {
    setError('')
    if (!open || !node || kind !== 'text') {
      setText('')
      setTruncated(false)
      setLoading(false)
      return
    }
    const controller = new AbortController()
    setText('')
    setTruncated(false)
    setLoading(true)
    fetch(previewUrl(node.id), {
      credentials: 'same-origin',
      cache: 'no-store',
      signal: controller.signal,
    })
      .then(async (res) => {
        if (!res.ok) throw new Error(await previewError(res))
        const body = await res.arrayBuffer()
        setText(new TextDecoder('utf-8').decode(body))
        setTruncated(res.headers.get('X-Preview-Truncated') === '1')
      })
      .catch((err) => {
        if (controller.signal.aborted) return
        setError(err instanceof Error ? err.message : '无法读取预览内容')
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false)
      })
    return () => controller.abort()
  }, [open, node, kind])

  function downloadButton(label = '下载原文件') {
    if (!node) return null
    return (
      <Button size="small" icon={<DownloadOutlined />} href={downloadUrl(node.id)}>
        {label}
      </Button>
    )
  }

  function renderBody() {
    if (!node || !kind || kind === 'pdf') return null
    if (kind === 'text') {
      if (loading) {
        return (
          <div className="preview-loading">
            <Spin />
          </div>
        )
      }
      if (error) {
        return <Alert type="error" showIcon message={error} action={downloadButton()} />
      }
      return (
        <>
          {truncated && (
            <Alert
              className="preview-alert"
              type="info"
              showIcon
              message="仅显示开头，完整内容请下载"
              action={downloadButton('下载完整文件')}
            />
          )}
          <pre className="preview-text">{text}</pre>
        </>
      )
    }
    if (error) {
      return <Alert type="warning" showIcon message={error} action={downloadButton()} />
    }
    if (kind === 'image') {
      return (
        <div className="preview-media">
          <img
            src={previewUrl(node.id)}
            alt={node.name}
            onError={() => setError('无法显示此图片，可能格式不受浏览器支持。请下载原文件查看。')}
          />
        </div>
      )
    }
    if (kind === 'video') {
      return (
        <div className="preview-media">
          <video
            controls
            playsInline
            preload="metadata"
            src={previewUrl(node.id)}
            onError={() => setError('浏览器无法播放此视频，可能编码不兼容。请下载原文件查看。')}
          />
        </div>
      )
    }
    return (
      <div className="preview-audio">
        <audio
          controls
          preload="metadata"
          src={previewUrl(node.id)}
          onError={() => setError('浏览器无法播放此音频，可能编码不兼容。请下载原文件查看。')}
        />
      </div>
    )
  }

  return (
    <Modal
      className="preview-modal"
      open={open}
      title={node ? `预览：${node.name}` : '预览'}
      width={920}
      footer={null}
      centered
      destroyOnClose
      onCancel={onClose}
    >
      <div className={`preview-body preview-kind-${kind || 'empty'}`}>{renderBody()}</div>
    </Modal>
  )
}
