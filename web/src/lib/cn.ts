// Joins class names, skipping falsy ones: cn("a", cond && "b"). Deliberately tiny; the design
// system does not need class merging because components take a className only for layout.
export function cn(...classes: (string | false | null | undefined)[]): string {
  return classes.filter(Boolean).join(" ");
}
