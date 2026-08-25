import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';

import { App } from './App';
import './index.css';

const container = document.getElementById('root');
if (container === null) {
  throw new Error('web ui: #root is missing from the served index.html');
}

createRoot(container).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
