import { DropdownMenu as DropdownMenuPrimitive } from "bits-ui";

import Content from "./dropdown-menu-content.svelte";
import Item from "./dropdown-menu-item.svelte";
import Separator from "./dropdown-menu-separator.svelte";

const Root = DropdownMenuPrimitive.Root;
const Trigger = DropdownMenuPrimitive.Trigger;
const Group = DropdownMenuPrimitive.Group;

export {
  Root,
  Root as DropdownMenu,
  Trigger,
  Trigger as DropdownMenuTrigger,
  Content,
  Content as DropdownMenuContent,
  Item,
  Item as DropdownMenuItem,
  Separator,
  Separator as DropdownMenuSeparator,
  Group,
  Group as DropdownMenuGroup
};
