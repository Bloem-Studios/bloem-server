import type { ReactNode } from "react";

import { cn } from "@/lib/utils";

// Bloem: the stream maps' button column and credit line (mapText.ts has the words).

export function MapButtons({ children }: { children: ReactNode }) {
  return <div className="absolute end-2 top-2 z-[1000] flex flex-col gap-1">{children}</div>;
}

export function MapButton({
  label,
  pressed,
  disabled,
  onClick,
  children,
}: {
  label: string;
  pressed?: boolean;
  disabled?: boolean;
  onClick: () => void;
  children: ReactNode;
}) {
  return (
    <button
      type="button"
      aria-label={label}
      title={label}
      aria-pressed={pressed}
      disabled={disabled}
      onClick={onClick}
      className={cn(
        "bg-background/80 text-foreground hover:bg-background grid size-7 cursor-pointer place-items-center rounded-md border shadow-sm backdrop-blur-sm transition-colors",
        "focus-visible:ring-ring focus-visible:ring-2 focus-visible:outline-none disabled:cursor-default disabled:opacity-50",
        pressed && "border-primary/50 bg-primary/15",
      )}
    >
      {children}
    </button>
  );
}

/** The imagery's credit, which its terms ask the map to show. */
export function MapCredit({ href, children }: { href: string; children: string }) {
  return (
    <a
      href={href}
      target="_blank"
      rel="noreferrer"
      className="bg-background/70 text-muted-foreground hover:text-foreground absolute end-2 bottom-1 z-[1000] rounded px-1 text-[0.625rem] backdrop-blur-sm"
    >
      {children}
    </a>
  );
}
