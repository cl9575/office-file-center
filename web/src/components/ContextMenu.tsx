import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { Menu } from 'antd'
import type { MenuProps } from 'antd'

export interface ContextMenuState {
  x: number
  y: number
  items: MenuProps['items']
  onSelect: (key: string) => void
}

export function ContextMenu({
  state,
  onClose,
}: {
  state: ContextMenuState | null
  onClose: () => void
}) {
  const ref = useRef<HTMLDivElement>(null)
  const [pos, setPos] = useState({ left: 0, top: 0 })

  useLayoutEffect(() => {
    if (!state) return
    const width = ref.current?.offsetWidth || 200
    const height = ref.current?.offsetHeight || 0
    setPos({
      left: Math.max(8, Math.min(state.x, window.innerWidth - width - 8)),
      top: Math.max(8, Math.min(state.y, window.innerHeight - height - 8)),
    })
  }, [state])

  useEffect(() => {
    if (!state) return
    const closeOnPointer = (event: PointerEvent) => {
      if (ref.current && event.target instanceof Node && ref.current.contains(event.target)) return
      onClose()
    }
    const closeOnKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') onClose()
    }
    window.addEventListener('pointerdown', closeOnPointer, true)
    window.addEventListener('keydown', closeOnKey)
    window.addEventListener('resize', onClose)
    window.addEventListener('scroll', onClose, true)
    return () => {
      window.removeEventListener('pointerdown', closeOnPointer, true)
      window.removeEventListener('keydown', closeOnKey)
      window.removeEventListener('resize', onClose)
      window.removeEventListener('scroll', onClose, true)
    }
  }, [state, onClose])

  if (!state) return null

  return createPortal(
    <div
      ref={ref}
      className="context-menu"
      style={{ left: pos.left, top: pos.top }}
      onContextMenu={(event) => event.preventDefault()}
    >
      <Menu
        items={state.items}
        onClick={({ key, domEvent }) => {
          domEvent.stopPropagation()
          state.onSelect(key)
          onClose()
        }}
      />
    </div>,
    document.body,
  )
}
