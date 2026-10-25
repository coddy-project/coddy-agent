Feature: A gallery of photos to pick a wallpaper from
  Next to a picture from the computer, the wallpaper block offers a gallery of dark
  Unsplash photos served by Lorem Picsum. Nothing is requested from the internet until the
  gallery is opened; a picked photo is downloaded once at the screen's size, kept in the
  browser like an uploaded picture, and credited to its author. Shuffle swaps the whole
  batch for twelve other photos of a larger hand-picked pool.

  Scenario: Opening the gallery
    Then nothing is requested from the gallery until it is opened
    And every gallery photo names its author and its Unsplash page

  Scenario: Picking a photo
    Then a picked photo becomes the wallpaper with its author's credit
    And the photo is asked for at the screen's size and never under the minimum
    And the author stays with the photo until it is removed

  Scenario: Shuffling the gallery
    Then Shuffle swaps the whole batch for new photos
    And a new batch is twelve pool photos not shown yet
    And a shuffle that fails says so and keeps the batch on screen

  Scenario: A photo that does not load
    Then a photo that does not load says so
