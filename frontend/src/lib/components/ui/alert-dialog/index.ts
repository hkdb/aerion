import { AlertDialog as AlertDialogPrimitive } from "bits-ui";

import Action from "./alert-dialog-action.svelte";
import Cancel from "./alert-dialog-cancel.svelte";
import Content from "./alert-dialog-content.svelte";
import Description from "./alert-dialog-description.svelte";
import Footer from "./alert-dialog-footer.svelte";
import Header from "./alert-dialog-header.svelte";
import Title from "./alert-dialog-title.svelte";
import Root from "./alert-dialog.svelte";

const Trigger = AlertDialogPrimitive.Trigger;
const Portal = AlertDialogPrimitive.Portal;

export {
  Root,
  Root as AlertDialog,
  Content,
  Content as AlertDialogContent,
  Header,
  Header as AlertDialogHeader,
  Footer,
  Footer as AlertDialogFooter,
  Title,
  Title as AlertDialogTitle,
  Description,
  Description as AlertDialogDescription,
  Action,
  Action as AlertDialogAction,
  Cancel,
  Cancel as AlertDialogCancel,
  Trigger,
  Trigger as AlertDialogTrigger,
  Portal,
  Portal as AlertDialogPortal
};
