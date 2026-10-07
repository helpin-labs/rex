import { createContext, type ReactNode, useContext } from "react";
import { createPortal } from "react-dom";

// The left pane is shared: on the Bots tab it lists bots, and on the other
// tabs (Memory, Routines, Skills, Settings) the page puts its own list
// there through SideList, so there is never a second list column.
export const SideSlot = createContext<HTMLElement | null>(null);

export function SideList({ children }: { children: ReactNode }) {
  const slot = useContext(SideSlot);
  return slot ? createPortal(<div className="side-page">{children}</div>, slot) : null;
}
