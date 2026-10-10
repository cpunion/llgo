typedef struct {
    long long s8;
    unsigned long long u8;
    long long s16;
    unsigned long long u16;
} narrow_values;

// This aggregate result also exercises the inserted sret parameter.
__attribute__((noinline)) narrow_values narrow_promote(signed char a, unsigned char b, short c, unsigned short d) {
    narrow_values result = {a, b, c, d};
    return result;
}

typedef narrow_values (*narrow_function)(signed char, unsigned char, short, unsigned short);
narrow_function narrow_address(void) { return narrow_promote; }
