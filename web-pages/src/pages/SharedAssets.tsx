import { StandaloneNativeAppPage } from '@saker/web-shared/pages'

export default function SharedAssets() {
  return <StandaloneNativeAppPage appId="assethub" apiBaseUrl="/v1" proxyHref="/assets" />
}
