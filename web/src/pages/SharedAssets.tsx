import { NativeAppPage } from '../../../../web-shared/src/pages'
import { createStandaloneHost, nativeRoutes } from '../../../../web-shared/src/runtime'

const route = nativeRoutes.find((item) => item.appId === 'assethub')

export default function SharedAssets() {
  if (!route) {
    return <p className="muted">Shared AssetHub route is not configured.</p>
  }
  const host = createStandaloneHost({ appId: 'assethub', apiBaseUrl: '/v1', proxyHref: '/assets' })
  return <NativeAppPage host={host} route={route} />
}
