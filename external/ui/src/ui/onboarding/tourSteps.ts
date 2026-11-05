export type TourStep = {
  id: string;
  anchor: string;
  titleKey: string;
  bodyKey: string;
  optional?: boolean;
};

/**
 * The tour in the order a first conversation happens: open a chat, find the
 * ones before it, settle the settings, then the folder it runs in, the field
 * the request is written in, the model that answers it, and send. The
 * scheduler is not part of a first conversation and has no step.
 */
export const TOUR_STEPS: TourStep[] = [
  {
    // Where every conversation begins.
    id: "newChat",
    anchor: '[data-testid="nav-new-chat"]',
    titleKey: "tour.step.newChat.title",
    bodyKey: "tour.step.newChat.body",
  },
  {
    id: "history",
    anchor: '[data-testid="nav-history"]',
    titleKey: "tour.step.history.title",
    bodyKey: "tour.step.history.body",
  },
  {
    id: "settings",
    anchor: '[data-testid="nav-settings"]',
    titleKey: "tour.step.settings.title",
    bodyKey: "tour.step.settings.body",
  },
  {
    // The folder a chat runs in; absent before a session exists.
    id: "project",
    anchor: '[data-testid="composer-workspace-chip"]',
    titleKey: "tour.step.project.title",
    bodyKey: "tour.step.project.body",
    optional: true,
  },
  {
    id: "input",
    anchor: "#composer",
    titleKey: "tour.step.input.title",
    bodyKey: "tour.step.input.body",
  },
  {
    // Absent until a model is configured, which a skipped picker leaves so.
    id: "model",
    anchor: ".mode-llm",
    titleKey: "tour.step.model.title",
    bodyKey: "tour.step.model.body",
    optional: true,
  },
  {
    id: "send",
    anchor: "#btn-send",
    titleKey: "tour.step.send.title",
    bodyKey: "tour.step.send.body",
  },
];
