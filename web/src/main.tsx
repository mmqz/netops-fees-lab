import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import App from './App';
import { ROOT_MOUNT_ERROR } from './i18n';
import './styles/global.css';

const rootElement = document.getElementById('root');
if (rootElement === null) {
  throw new Error(ROOT_MOUNT_ERROR);
}

createRoot(rootElement).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
