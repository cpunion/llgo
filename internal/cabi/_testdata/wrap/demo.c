/* This is a calling-convention fixture, not an external-I/O fixture. */

int demo32(int v) {
    return v+100;
}

long long demo64(long long v) {
    return v+100;
}

struct struct32 {
     int v;
};

struct point64 {
    long long x;
    long long y;
};

struct point64 pt64(struct point64 pt) {
    return pt;
}

struct struct32 demo32s(struct struct32 v) {
    struct struct32 v2 = {v.v+100};
    return v2;
}

struct point {
    int x;
    int y;
};

struct point pt(struct point pt) {
    return pt;
}

struct point1 {
    int x;
    int y;
    int z;
};

struct point1 pt1(struct point1 pt) {
    return pt;
}

struct point2 {
    char x;
    int y;
    int z;
};

struct point2 pt2(struct point2 pt) {
    return pt;
}

struct point3 {
    char x;
    char y;
    char z;
};

struct point3 pt3(struct point3 pt) {
    return pt;
}

struct point4 {
    char x;
    char y;
    char z;
    int  m;
};

struct point4 pt4(struct point4 pt) {
    return pt;
}

struct point5 {
    char x;
    char y;
    char z;
    char m;
    char n;
};

struct point5 pt5(struct point5 pt) {
    return pt;
}

struct point6 {
    char x;
    char y;
    char z;
    char m;
    char n;
    int  k;
};

struct point6 pt6(struct point6 pt) {
    return pt;
}

struct point7 {
    char x;
    char y;
    char z;
    char m;
    char n;
    int  k;
    char o;
};

struct point7 pt7(struct point7 pt) {
    return pt;
}

struct data1 {
    char x;
    long long y;
};

struct data1 fn1(struct data1 pt) {
    return pt;
}

struct data2 {
    int x;
    long long y;
};

struct data2 fn2(struct data2 pt) {
    return pt;
}

struct data3 {
    long long x;
    char y;
};

struct data3 fn3(struct data3 pt) {
    return pt;
}

struct fdata1 {
    float x;
};

struct fdata1 ff1(struct fdata1 pt) {
    return pt;
}

struct ddata1 {
    double x;
};

struct ddata1 dd1(struct ddata1 pt) {
    return pt;
}

struct ddata2 {
    double x;
    double y;
};

struct ddata2 dd2(struct ddata2 pt) {
    return pt;
}

struct ddata3 {
    double x;
    double y;
    double z;
};

struct ddata3 dd3(struct ddata3 pt) {
    return pt;
}

struct fdata2i {
    float x;
    int   y;
};

struct fdata2i ff2i(struct fdata2i pt) {
    return pt;
}

struct fdata2 {
    float x;
    float y;
};

struct fdata2 ff2(struct fdata2 pt) {
    return pt;
}

struct fdata3 {
    float x;
    float y;
    float z;
};

struct fdata3 ff3(struct fdata3 pt) {
    return pt;
}

struct fdata4 {
    float x;
    float y;
    float z;
    float m;
};

struct fdata4 ff4(struct fdata4 pt) {
    return pt;
}

struct fdata5 {
    float x;
    float y;
    float z;
    float m;
    float n;
};

struct fdata5 ff5(struct fdata5 pt) {
    return pt;
}

struct fdata2id {
    char     x;
    char     y;
    double   z;
};

struct fdata2id ff2id(struct fdata2id pt) {
    return pt;
}

struct fdata7if {
    char    x[7];
    float    z;
};

struct fdata7if ff7if(struct fdata7if pt) {
    return pt;
}

struct fdata4if {
    float    x;
    char     y;
    float    z;
    float    m;
};

struct fdata4if ff4if(struct fdata4if pt) {
    return pt;
}

struct array {
    int x[8];
};

struct array demo(struct array a) {
    return a;
}

struct array demo2(int a1){
    struct array x;
    for (int i = 0; i < 8; i++) {
        x.x[i] = i+a1;
    }
    return x;
}

void callback(struct array (*fn)(struct array ar, struct point pt, struct point1 pt1), struct array ar) {
    demo(ar);
    struct point pt = {1,2};
    struct point1 pt1 = {1,2,3};
    struct array ret = fn(ar,pt,pt1);
    demo(ret);
}

void callback1(struct point (*fn)(struct array ar, struct point pt, struct point1 pt1), struct array ar) {
    struct point pt = {1,2};
    struct point1 pt1 = {1,2,3};
    struct point ret = fn(ar,pt,pt1);
}

struct point mycallback(struct array ar, struct point pt, struct point1 pt1) {
    struct point ret = {pt.x+pt1.x, pt.y+pt1.y};
    return ret;
}
