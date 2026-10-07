// Imported first by main.tsx: Telegram's launch parameters are read out of the
// address before any other module can see or rewrite the fragment.
import { captureTelegramLaunch } from "./launch";

captureTelegramLaunch();
