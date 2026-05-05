import { ContextMenu as ContextMenuPrimitive } from "bits-ui";

import Content from "./context-menu-content.svelte";
import Item from "./context-menu-item.svelte";
import Separator from "./context-menu-separator.svelte";
import SubContent from "./context-menu-sub-content.svelte";
import SubTrigger from "./context-menu-sub-trigger.svelte";

const Root = ContextMenuPrimitive.Root;
const Trigger = ContextMenuPrimitive.Trigger;
const Group = ContextMenuPrimitive.Group;
const Sub = ContextMenuPrimitive.Sub;

export {
  Root,
  Root as ContextMenu,
  Trigger,
  Trigger as ContextMenuTrigger,
  Content,
  Content as ContextMenuContent,
  Item,
  Item as ContextMenuItem,
  Separator,
  Separator as ContextMenuSeparator,
  Sub,
  Sub as ContextMenuSub,
  SubTrigger,
  SubTrigger as ContextMenuSubTrigger,
  SubContent,
  SubContent as ContextMenuSubContent,
  Group,
  Group as ContextMenuGroup
};
