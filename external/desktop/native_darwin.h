//go:build desktop && desktop.native && darwin && cgo

#ifndef CODDY_NATIVE_DARWIN_H
#define CODDY_NATIVE_DARWIN_H
#include <stdint.h>
typedef struct { int x, y, width, height; } CoddyFrame;

int coddy_native_available(void);
int coddy_native_main_thread(void);
void *coddy_native_create(const char *url, const char *store, int framed, int minimised, int min_width, int min_height);
void coddy_native_run(void *window);
void coddy_native_release(void *window);
void coddy_native_action(void *window, uintptr_t callback, int action, int x, int y);
// Frame primitives run only within the main-thread geometry callback.
CoddyFrame coddy_native_frame(void *window);
void coddy_native_set_frame(void *window, CoddyFrame frame);
int coddy_native_zoomed(void *window);
void coddy_native_unzoom(void *window);

enum {
    CoddyUndecorate, CoddyDecorate, CoddyMinimise, CoddyMaximise, CoddyClose,
    CoddyActivate, CoddyMoveBegin, CoddyMoveBy, CoddyResizeBegin,
    CoddyResizeN, CoddyResizeS, CoddyResizeW, CoddyResizeE,
    CoddyResizeNW, CoddyResizeNE, CoddyResizeSW, CoddyResizeSE
};
#endif
