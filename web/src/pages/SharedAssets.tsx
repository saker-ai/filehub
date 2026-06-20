import { StandaloneNativeAppPage } from '../../../../web-shared/src/pages'

export default function SharedAssets() {
  return <StandaloneNativeAppPage appId="assethub" apiBaseUrl="/v1" proxyHref="/assets" />
}
