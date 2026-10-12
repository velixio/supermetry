import { eventsEndpoint, scriptSource, type InitOptions, type TrackerOptions } from "@supermetry/tracker";

export type SupermetryProps = Omit<TrackerOptions, "beforeSend"> & {
  site: string;
  host?: string;
  src?: string;
  beforeSend?: string;
  viewTransitions?: boolean;
};

export function scriptAttributes(props: SupermetryProps): Record<string, string | boolean> {
  if (!props?.site) throw new TypeError("@supermetry/astro requires site");
  const manual = props.viewTransitions ? true : props.manual;
  const options = { ...props, host: props.host ?? "", manual, beforeSend: props.beforeSend ? () => null : undefined } as InitOptions;
  const attributes: Record<string, string | boolean> = {
    async: true,
    defer: true,
    src: scriptSource(options),
    "data-site": props.site,
  };
  const api = eventsEndpoint(options);
  if (api !== undefined) attributes["data-api"] = api;
  const flags = { manual, hash: props.hash, forms: props.forms, outbound: props.outbound, downloads: props.downloads, profiles: props.profiles };
  for (const [name, value] of Object.entries(flags)) {
    if (value !== undefined) attributes[`data-${name}`] = String(value);
  }
  if (props.consent) attributes["data-consent"] = "required";
  if (props.beforeSend) attributes["data-before-send"] = props.beforeSend;
  if (props.allowLocalhost) attributes["data-allow-localhost"] = "";
  return attributes;
}

export const pageLoadScript =
  '(()=>{if(window.__supermetryPageLoad)return;window.__supermetryPageLoad=1;document.addEventListener("astro:page-load",()=>{var u=location.href,n=0,s=()=>{var t=window.supermetry;t&&t.pageview?t.pageview({url:u}):++n<200&&setTimeout(s,50)};s()})})()';
