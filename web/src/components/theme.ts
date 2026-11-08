import { createContext } from "react";

/** ThemeContext is the effective theme, "light" or "dark", for components that draw with canvas. */
export const ThemeContext = createContext("light");
