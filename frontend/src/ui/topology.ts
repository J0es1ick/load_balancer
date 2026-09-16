export class TopologyDiagram {
  private resizeObserver: ResizeObserver | null = null
  private frame = 0
  private canvas: HTMLElement | null = null
  private observed = new Set<HTMLElement>()
  private signature = ''

  connect(): void {
    const canvas = document.querySelector<HTMLElement>('#topology-canvas')
    if (canvas !== this.canvas) {
      this.disconnect()
      this.canvas = canvas
      if (!canvas) return
      this.resizeObserver = new ResizeObserver(() => this.queueDraw())
      this.resizeObserver.observe(canvas)
    }
    if (!canvas) return
    const nodes = new Set(canvas.querySelectorAll<HTMLElement>('[data-endpoint-key], .topology-node'))
    for (const element of this.observed) {
      if (!nodes.has(element)) this.resizeObserver?.unobserve(element)
    }
    for (const element of nodes) {
      if (!this.observed.has(element)) this.resizeObserver?.observe(element)
    }
    this.observed = nodes
    const signature = Array.from(nodes, (node) => `${node.dataset.endpointKey ?? node.id}:${node.classList.contains('endpoint--ok')}`).join('|')
    if (signature !== this.signature) {
      this.signature = signature
      this.queueDraw()
    }
  }

  disconnect(): void {
    this.resizeObserver?.disconnect()
    this.resizeObserver = null
    this.canvas = null
    this.observed.clear()
    this.signature = ''
    window.cancelAnimationFrame(this.frame)
  }

  pulse(endpointKey: string): void {
    const svg = document.querySelector<SVGSVGElement>('#topology-lines')
    const path = svg?.querySelector<SVGPathElement>(`[data-line-key="${CSS.escape(endpointKey)}"]`)
    if (!svg || !path) return
    const circle = document.createElementNS('http://www.w3.org/2000/svg', 'circle')
    circle.setAttribute('r', '5')
    circle.setAttribute('class', 'request-pulse')
    const motion = document.createElementNS('http://www.w3.org/2000/svg', 'animateMotion')
    motion.setAttribute('dur', '700ms')
    motion.setAttribute('fill', 'freeze')
    motion.setAttribute('path', path.getAttribute('d') ?? '')
    circle.append(motion)
    svg.append(circle)
    motion.beginElement()
    window.setTimeout(() => circle.remove(), 850)
  }

  private queueDraw(): void {
    window.cancelAnimationFrame(this.frame)
    this.frame = window.requestAnimationFrame(() => this.draw())
  }

  private draw(): void {
    const canvas = document.querySelector<HTMLElement>('#topology-canvas')
    const svg = document.querySelector<SVGSVGElement>('#topology-lines')
    const client = document.querySelector<HTMLElement>('#topology-client')
    const gateway = document.querySelector<HTMLElement>('#topology-gateway')
    if (!canvas || !svg || !client || !gateway) return
    const canvasRect = canvas.getBoundingClientRect()
    if (canvasRect.width < 1 || canvasRect.height < 1) return
    const viewBox = `0 0 ${canvasRect.width} ${canvasRect.height}`
    if (svg.getAttribute('viewBox') !== viewBox) svg.setAttribute('viewBox', viewBox)
    svg.setAttribute('preserveAspectRatio', 'xMidYMid meet')
    const unusedPaths = new Set(svg.querySelectorAll<SVGPathElement>('path[data-line-key]'))

    const point = (element: HTMLElement, edge: 'start' | 'end') => {
      const rect = element.getBoundingClientRect()
      const vertical = window.matchMedia('(max-width: 820px)').matches
      return vertical
        ? { x: rect.left - canvasRect.left + rect.width / 2, y: (edge === 'start' ? rect.bottom : rect.top) - canvasRect.top }
        : { x: (edge === 'start' ? rect.right : rect.left) - canvasRect.left, y: rect.top - canvasRect.top + rect.height / 2 }
    }
    const pathBetween = (from: { x: number; y: number }, to: { x: number; y: number }): string => {
      const vertical = window.matchMedia('(max-width: 820px)').matches
      if (vertical) {
        const middle = from.y + (to.y - from.y) * 0.5
        return `M ${from.x} ${from.y} C ${from.x} ${middle}, ${to.x} ${middle}, ${to.x} ${to.y}`
      }
      const middle = from.x + (to.x - from.x) * 0.48
      return `M ${from.x} ${from.y} C ${middle} ${from.y}, ${middle} ${to.y}, ${to.x} ${to.y}`
    }
    const addPath = (d: string, className: string, key: string) => {
      let path = Array.from(unusedPaths).find((element) => element.dataset.lineKey === key)
      if (path) unusedPaths.delete(path)
      else {
        path = document.createElementNS('http://www.w3.org/2000/svg', 'path')
        path.dataset.lineKey = key
        svg.prepend(path)
      }
      if (path.getAttribute('d') !== d) path.setAttribute('d', d)
      if (path.getAttribute('class') !== className) path.setAttribute('class', className)
    }
    addPath(pathBetween(point(client, 'start'), point(gateway, 'end')), 'topology-line topology-line--ingress', 'ingress')
    document.querySelectorAll<HTMLElement>('[data-endpoint-key]').forEach((endpoint) => {
      const key = endpoint.dataset.endpointKey ?? ''
      addPath(pathBetween(point(gateway, 'start'), point(endpoint, 'end')), `topology-line ${endpoint.classList.contains('endpoint--ok') ? 'is-ready' : ''}`, key)
    })
    for (const path of unusedPaths) path.remove()
  }
}
