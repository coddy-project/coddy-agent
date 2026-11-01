// Fills the hooks the OK, coddy panel declares: one more trick button and a
// footer note. The panel does not know this extension exists.
export function activate(coddy) {
  coddy.i18n.registerMessages("en", {
    trick: "disco",
    note: "Trick and note contributed by ok-coddy-addon",
  });
  coddy.i18n.registerMessages("ru", {
    trick: "диско",
    note: "Трюк и подпись от ok-coddy-addon",
  });
  const tricks = coddy.hooks.register({
    id: "disco-trick",
    into: "ok-coddy/tricks",
    mount(element) {
      const button = document.createElement("button");
      button.className = "settings-btn ok-trick-btn";
      button.textContent = coddy.i18n.t("trick");
      button.addEventListener(
        "click",
        () => {
          const duck = document.querySelector(".ok-duck");
          if (duck) {
            duck.classList.remove(
              ...[...duck.classList].filter((c) => c.indexOf("trick-") === 0),
            );
            void duck.offsetWidth;
            duck.classList.add("trick-dance");
          }
        },
        { signal: coddy.signal },
      );
      element.append(button);
    },
  });
  const footer = coddy.hooks.register({
    id: "note",
    into: "ok-coddy/footer",
    mount(element) {
      const line = document.createElement("p");
      line.className = "ok-line ok-line-note";
      line.textContent = coddy.i18n.t("note");
      element.append(line);
    },
  });
  // A control in the composer's pick strip, and an alter pass over the
  // assembled control list - the declare-then-alter pipeline.
  const control = coddy.ui.registerComposerControl({
    id: "disco-quick",
    placement: "tabs",
    title: "Disco",
    mount(element) {
      const button = document.createElement("button");
      button.type = "button";
      button.className = "settings-btn";
      button.textContent = coddy.i18n.t("trick");
      button.addEventListener(
        "click",
        () => {
          const duck = document.querySelector(".ok-duck");
          if (duck) {
            duck.classList.remove(
              ...[...duck.classList].filter((c) => c.indexOf("trick-") === 0),
            );
            void duck.offsetWidth;
            duck.classList.add("trick-dance");
          }
        },
        { signal: coddy.signal },
      );
      element.append(button);
    },
  });
  const alter = coddy.ui.alterControls((controls) =>
    controls.map((row) =>
      row.id === "disco-quick" ? { ...row, title: "Disco trick" } : row,
    ),
  );
  return () => {
    alter.remove();
    control.remove();
    tricks.remove();
    footer.remove();
  };
}
