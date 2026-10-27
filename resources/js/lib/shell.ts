import { createContext, useContext } from 'react';

// ShellContext lets a page's top bar open the layout's navigation drawer on
// phones (AppLayout provides it, Page uses it).
export const ShellContext = createContext<{ openMenu: () => void }>({ openMenu: () => {} });

export const useShell = () => useContext(ShellContext);
