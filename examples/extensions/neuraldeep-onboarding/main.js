const PROVIDER = "neuraldeep";
const TYPE = "neuraldeep";
const MODEL = "neuraldeep/qwen3.6-35b-a3b";
const HUB = "https://hub.neuraldeep.ru";

// The provider's proxy setting is one URL; credentials ride in it.
function composeProxy(address, user, pass) {
  const raw = String(address || "").trim();
  if (!raw) return "";
  const url = /^[a-z][a-z0-9+.-]*:\/\//i.test(raw) ? raw : "http://" + raw;
  if (!user && !pass) return url;
  try {
    const parsed = new URL(url);
    if (user) parsed.username = user;
    if (pass) parsed.password = pass;
    return parsed.toString();
  } catch (e) {
    return url;
  }
}

export function activate(coddy) {
  coddy.i18n.registerMessages("en", {
    title: "Connect NeuralDeep",
    intro:
      "Coddy can run the qwen3.6-35b-a3b model through the NeuralDeep API. Get a key, paste it here, and the provider is written to your configuration.",
    getKey: "Get a key at hub.neuraldeep.ru",
    mirror:
      "Outside Russia the mirror is hub.neuraldeep.tech - a key of one hub is not honored by the other.",
    key: "API key",
    create: "Create provider",
    skip: "Skip",
    working: "Writing the configuration…",
    ready: "Provider ready: the model " + MODEL + " is configured.",
    again: "NeuralDeep setup",
    needKey: "Enter the key first.",
    failed: "Could not write the configuration: ",
    proxyTitle: "Corporate proxy (optional)",
    proxy: "Proxy address",
    proxyHint: "host:port or a full URL - written to the provider's proxy setting.",
    proxyUser: "Proxy login",
    proxyPass: "Proxy password",
  });
  coddy.i18n.registerMessages("ru", {
    title: "Подключение NeuralDeep",
    intro:
      "Coddy может работать с моделью qwen3.6-35b-a3b через API NeuralDeep. Оформите ключ, вставьте его сюда, и провайдер запишется в конфигурацию.",
    getKey: "Оформить ключ на hub.neuraldeep.ru",
    mirror:
      "Вне России работает зеркало hub.neuraldeep.tech - ключ одного хаба не принимается другим.",
    key: "Ключ API",
    create: "Создать провайдера",
    skip: "Пропустить",
    working: "Записываю конфигурацию…",
    ready: "Провайдер готов: модель " + MODEL + " настроена.",
    again: "Настройка NeuralDeep",
    needKey: "Сначала введите ключ.",
    failed: "Не удалось записать конфигурацию: ",
    proxyTitle: "Корпоративный прокси (необязательно)",
    proxy: "Адрес прокси",
    proxyHint: "host:port или полный URL - запишется в настройку proxy провайдера.",
    proxyUser: "Логин прокси",
    proxyPass: "Пароль прокси",
  });

  let panel = null;
  let item = null;
  let titleWatch = null;

  const openSetup = () => {
    if (panel) return;
    panel = coddy.ui.registerPanel({
      id: "neuraldeep-setup",
      title: coddy.i18n.t("title"),
      placement: "full",
      mount(element) {
        const box = document.createElement("section");
        box.className = "ext-neuraldeep";

        const intro = document.createElement("p");
        intro.textContent = coddy.i18n.t("intro");
        const link = document.createElement("a");
        link.href = HUB;
        link.target = "_blank";
        link.rel = "noreferrer";
        link.textContent = coddy.i18n.t("getKey");
        const mirror = document.createElement("p");
        mirror.className = "ext-neuraldeep-note";
        mirror.textContent = coddy.i18n.t("mirror");

        const label = document.createElement("label");
        label.textContent = coddy.i18n.t("key");
        const input = document.createElement("input");
        input.type = "password";
        input.autocomplete = "off";
        input.className = "ext-neuraldeep-key";

        const proxyTitle = document.createElement("h3");
        proxyTitle.textContent = coddy.i18n.t("proxyTitle");
        const proxyLabel = document.createElement("label");
        proxyLabel.textContent = coddy.i18n.t("proxy");
        const proxyInput = document.createElement("input");
        proxyInput.type = "text";
        proxyInput.autocomplete = "off";
        proxyInput.className = "ext-neuraldeep-key";
        const proxyHint = document.createElement("p");
        proxyHint.className = "ext-neuraldeep-note";
        proxyHint.textContent = coddy.i18n.t("proxyHint");
        const proxyUserLabel = document.createElement("label");
        proxyUserLabel.textContent = coddy.i18n.t("proxyUser");
        const proxyUser = document.createElement("input");
        proxyUser.type = "text";
        proxyUser.autocomplete = "off";
        proxyUser.className = "ext-neuraldeep-key";
        const proxyPassLabel = document.createElement("label");
        proxyPassLabel.textContent = coddy.i18n.t("proxyPass");
        const proxyPass = document.createElement("input");
        proxyPass.type = "password";
        proxyPass.autocomplete = "off";
        proxyPass.className = "ext-neuraldeep-key";

        const status = document.createElement("p");
        status.className = "ext-neuraldeep-note";
        const create = document.createElement("button");
        create.className = "settings-btn";
        create.textContent = coddy.i18n.t("create");
        const skip = document.createElement("button");
        skip.className = "settings-btn";
        skip.textContent = coddy.i18n.t("skip");
        const actions = document.createElement("div");
        actions.className = "ext-neuraldeep-actions";

        const close = () => {
          const open = panel;
          panel = null;
          if (open) open.remove();
        };
        create.addEventListener(
          "click",
          () => {
            const key = input.value.trim();
            if (!key) {
              status.textContent = coddy.i18n.t("needKey");
              return;
            }
            create.disabled = true;
            status.textContent = coddy.i18n.t("working");
            coddy.config
              .get("providers")
              .then((providers) => {
                const rows = Array.isArray(providers) ? providers : [];
                const existing = rows.findIndex(
                  (row) => row && (row.name === PROVIDER || row.type === TYPE),
                );
                const row = { name: PROVIDER, type: TYPE, api_key: key };
                const proxy = composeProxy(
                  proxyInput.value,
                  proxyUser.value,
                  proxyPass.value,
                );
                if (proxy) row.proxy = proxy;
                return coddy.config.set(
                  existing >= 0 ? "providers." + existing : "providers.+",
                  row,
                );
              })
              .then(() => coddy.config.get("models"))
              .then((models) => {
                const rows = Array.isArray(models) ? models : [];
                const has = rows.some((row) => row && row.model === MODEL);
                return has
                  ? null
                  : coddy.config.set("models.+", { model: MODEL });
              })
              .then(() => {
                status.textContent = coddy.i18n.t("ready");
                create.disabled = false;
                window.setTimeout(close, 1200);
              })
              .catch((error) => {
                create.disabled = false;
                status.textContent = coddy.i18n.t("failed") + String(error);
              });
          },
          { signal: coddy.signal },
        );
        skip.addEventListener("click", close, { signal: coddy.signal });

        actions.append(create, skip);
        box.append(
          intro,
          link,
          mirror,
          label,
          input,
          proxyTitle,
          proxyLabel,
          proxyInput,
          proxyHint,
          proxyUserLabel,
          proxyUser,
          proxyPassLabel,
          proxyPass,
          status,
          actions,
        );
        element.append(box);
        input.focus();
        return () => {
          if (titleWatch) {
            titleWatch.remove();
            titleWatch = null;
          }
          panel = null;
        };
      },
    });
    titleWatch = coddy.i18n.onChange(() => {
      if (panel) panel.update({ title: coddy.i18n.t("title") });
    });
    coddy.ui.openPanel("neuraldeep-setup");
  };

  const ready = () =>
    coddy.config
      .get("providers")
      .then((providers) => {
        const rows = Array.isArray(providers) ? providers : [];
        return rows.some(
          (row) => row && (row.name === PROVIDER || row.type === TYPE),
        );
      })
      .catch(() => true);

  ready().then((hasProvider) => {
    if (!hasProvider) openSetup();
    item = coddy.ui.registerMenuItem({
      id: "neuraldeep-setup",
      title: coddy.i18n.t("again"),
      onSelect: openSetup,
    });
  });

  return () => {
    if (titleWatch) titleWatch.remove();
    if (item) item.remove();
    if (panel) panel.remove();
  };
}
