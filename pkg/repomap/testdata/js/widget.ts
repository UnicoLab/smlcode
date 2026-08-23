import { helper } from "./helper";

export interface WidgetProps {
  title: string;
}

export type WidgetState = "idle" | "busy";

export class Widget {
  constructor(private props: WidgetProps) {}
}

export function renderWidget(props: WidgetProps): string {
  return helper(props.title);
}

export const useWidget = (props: WidgetProps) => new Widget(props);

function internalOnly() {
  return 1;
}
