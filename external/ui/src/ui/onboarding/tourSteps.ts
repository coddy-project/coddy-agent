export type TourStep = {
  id: string;
  anchor: string;
  titleKey: string;
  bodyKey: string;
  optional?: boolean;
};

export const TOUR_STEPS: TourStep[] = [
  {
    id: "settings",
    anchor: '[data-testid="nav-settings"]',
    titleKey: "tour.step.settings.title",
    bodyKey: "tour.step.settings.body",
  },
  {
    id: "project",
    anchor: '[data-testid="composer-workspace-chip"]',
    titleKey: "tour.step.project.title",
    bodyKey: "tour.step.project.body",
    optional: true,
  },
  {
    id: "history",
    anchor: '[data-testid="nav-history"]',
    titleKey: "tour.step.history.title",
    bodyKey: "tour.step.history.body",
  },
  {
    id: "scheduler",
    anchor: '[data-testid="nav-scheduler"]',
    titleKey: "tour.step.scheduler.title",
    bodyKey: "tour.step.scheduler.body",
    optional: true,
  },
  {
    id: "model",
    anchor: ".mode-llm",
    titleKey: "tour.step.model.title",
    bodyKey: "tour.step.model.body",
    optional: true,
  },
  {
    id: "input",
    anchor: "#composer",
    titleKey: "tour.step.input.title",
    bodyKey: "tour.step.input.body",
  },
  {
    id: "send",
    anchor: "#btn-send",
    titleKey: "tour.step.send.title",
    bodyKey: "tour.step.send.body",
  },
];
