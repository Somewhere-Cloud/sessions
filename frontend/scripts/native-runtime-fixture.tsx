import { createRoot } from 'react-dom/client';
import { NativeRuntimeStatusCard } from '../src/components/NativeRuntimeStatusCard';
import '../src/styles/globals.css';

createRoot(document.getElementById('root')!).render(<NativeRuntimeStatusCard />);
