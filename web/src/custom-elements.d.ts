import type React from 'react'

declare global {
  namespace JSX {
    interface IntrinsicElements {
      'model-viewer': React.DetailedHTMLProps<React.HTMLAttributes<HTMLElement>, HTMLElement> & {
        src?: string
        'camera-controls'?: boolean
        ar?: boolean
        'auto-rotate'?: boolean
      }
    }
  }
}
