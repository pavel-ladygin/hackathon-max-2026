export type MaxPlatformName = "ios" | "android" | "desktop" | "web" | (string & {});

export type MaxViewport = {
  width: number;
  height: number;
};

export type GeoPosition = { lat: number; lng: number; accuracyM: number | null };

export type MaxEnvironment = "max" | "browser";

export type InviteSharePayload = {
  link: string;
  text?: string;
};

export type MaxPlatformSnapshot = {
  environment: MaxEnvironment;
  isMax: boolean;
  initData: string;
  /** Untrusted hint intended only for routing/preview. */
  startParam: string | null;
  platform: MaxPlatformName | null;
  version: string | null;
  viewport: MaxViewport | null;
};

export interface MaxPlatformAdapter {
  readonly environment: MaxEnvironment;
  readonly isMax: boolean;
  /** Raw signed data. Pass to the server; do not parse or persist it in the client. */
  getInitData(): string;
  /** Untrusted launch hint. The server remains the source of truth. */
  getStartParam(): string | null;
  getPlatform(): MaxPlatformName | null;
  getVersion(): string | null;
  getViewport(): Promise<MaxViewport>;
  ready(): void;
  shareInvite(payload: InviteSharePayload): Promise<boolean>;
  openMaxLink(url: string): Promise<boolean>;
  openTicketLink(url: string): Promise<boolean>;
  requestLocation(): Promise<GeoPosition | null>;
  copyText(value: string): Promise<boolean>;
  setClosingConfirmation(enabled: boolean): void;
  snapshot(): MaxPlatformSnapshot;
}
