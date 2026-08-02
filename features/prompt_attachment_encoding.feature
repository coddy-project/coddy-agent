Feature: Prompt attachments in non-UTF-8 encodings
  A workspace text file may be stored in a legacy encoding - Windows-1251 is the
  usual case on a Russian Windows install. Attaching such a file through the
  composer's @ picker, or naming it as a bare @path inside the prompt text,
  hydrates its content into the prompt: the bytes are decoded to UTF-8 so the
  model reads the real text. Files that already are UTF-8 pass through untouched.

  Scenario: Attaching a Windows-1251 file hydrates readable text
    Given a workspace file "notes.txt" encoded in windows-1251 with content:
      """
      Привет, мир!
      Это заметка в кодировке Windows-1251, сохранённая блокнотом.
      Проверяем, что вложение доходит до модели без ошибок.
      """
    When I attach "notes.txt" to the prompt "посмотри @notes.txt"
    Then the prompt has a resource for "notes.txt"
    And the resource mime type is "text/plain; charset=utf-8"
    And the resource text is:
      """
      Привет, мир!
      Это заметка в кодировке Windows-1251, сохранённая блокнотом.
      Проверяем, что вложение доходит до модели без ошибок.
      """

  Scenario: A bare @mention of a Windows-1251 file hydrates readable text
    Given a workspace file "readme.txt" encoded in windows-1251 with content:
      """
      Описание проекта на русском языке.
      Файл сохранён в однобайтовой кодировке Windows-1251.
      Агент должен увидеть текст, а не сообщение об ошибке.
      """
    When I hydrate the prompt text "прочитай @readme.txt и перескажи"
    Then the prompt has a resource for "readme.txt"
    And the resource mime type is "text/plain; charset=utf-8"
    And the resource text is:
      """
      Описание проекта на русском языке.
      Файл сохранён в однобайтовой кодировке Windows-1251.
      Агент должен увидеть текст, а не сообщение об ошибке.
      """

  Scenario: Attaching a UTF-8 file still hydrates it unchanged
    Given a workspace file "utf8.md" encoded in utf-8 with content:
      """
      # Заголовок
      Обычный UTF-8 файл со смешанным текстом and English words.
      """
    When I attach "utf8.md" to the prompt "see @utf8.md"
    Then the prompt has a resource for "utf8.md"
    And the resource mime type is "text/plain; charset=utf-8"
    And the resource text is:
      """
      # Заголовок
      Обычный UTF-8 файл со смешанным текстом and English words.
      """
