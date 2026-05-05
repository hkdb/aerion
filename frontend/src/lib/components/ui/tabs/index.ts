import { Tabs as TabsPrimitive } from "bits-ui";

import Content from "./tabs-content.svelte";
import List from "./tabs-list.svelte";
import Trigger from "./tabs-trigger.svelte";
import Root from "./tabs.svelte";

export {
  Root,
  List,
  Trigger,
  Content,
  //
  Root as Tabs,
  List as TabsList,
  Trigger as TabsTrigger,
  Content as TabsContent
};
