import { type ComponentProps, useEffect, useState } from "react";

type CardModule = typeof import("./OperatorCommandCard");

let loaded: CardModule | null = null;
let loading: Promise<CardModule> | null = null;

/**
 * Loads the operator command card's chunk. The card shows only when the agent
 * asks the operator to run something, so it stays out of app.js; once the
 * chunk is in, every later card renders at once.
 */
export function loadOperatorCommandCard(): Promise<CardModule> {
  if (loaded) return Promise.resolve(loaded);
  loading ??= import("./OperatorCommandCard").then((m) => {
    loaded = m;
    return m;
  });
  return loading;
}

type Props = ComponentProps<CardModule["OperatorCommandCard"]>;

/** The card, rendered as soon as its chunk is there (no Suspense boundary). */
export function OperatorCommandCardLazy(props: Props) {
  const [mod, setMod] = useState<CardModule | null>(loaded);
  useEffect(() => {
    if (mod) return;
    let live = true;
    void loadOperatorCommandCard().then((m) => {
      if (live) setMod(m);
    });
    return () => {
      live = false;
    };
  }, [mod]);
  if (!mod) return null;
  return <mod.OperatorCommandCard {...props} />;
}
