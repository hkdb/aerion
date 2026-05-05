import { Dialog as DialogPrimitive } from "bits-ui";

import Content from "./dialog-content.svelte";
import Description from "./dialog-description.svelte";
import Footer from "./dialog-footer.svelte";
import Header from "./dialog-header.svelte";
import Overlay from "./dialog-overlay.svelte";
import Title from "./dialog-title.svelte";
import Root from "./dialog.svelte";

const Trigger = DialogPrimitive.Trigger;
const Close = DialogPrimitive.Close;
const Portal = DialogPrimitive.Portal;

export {
  Root,
  Root as Dialog,
  Content,
  Content as DialogContent,
  Header,
  Header as DialogHeader,
  Footer,
  Footer as DialogFooter,
  Title,
  Title as DialogTitle,
  Description,
  Description as DialogDescription,
  Overlay,
  Overlay as DialogOverlay,
  Trigger,
  Trigger as DialogTrigger,
  Close,
  Close as DialogClose,
  Portal,
  Portal as DialogPortal
};
